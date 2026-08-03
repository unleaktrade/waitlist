package airdrop

import (
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
)

var (
	ErrJobExists  = errors.New("airdrop job already exists for this address")
	ErrJobMissing = errors.New("no airdrop job for this address")
	ErrNotClaimed = errors.New("airdrop job not claimable")
)

// Store persists distribution jobs. Conditional writes provide both
// idempotency (Enqueue) and safe concurrent claiming (Claim).
type Store interface {
	Enqueue(j *Job) error
	Get(address string) (*Job, error)
	Claim(address string, now, leaseUntil int64) (*Job, error)
	Release(address string, nextAttemptAt int64, code, msg string) error
	MarkProcessingSignature(address, sig string) error
	MarkConfirmed(address, sig string) error
	MarkTerminal(address, code, msg string) error
	ResetForRetry(address string) error
	ListActionable(now int64) ([]*Job, error)
	ListAll() ([]*Job, error)
}

type dynamoStore struct {
	tn string
}

// NewDynamoStore returns the DynamoDB-backed Store. The table has a single
// partition key "address" (S), like the Waitlist table.
func NewDynamoStore(tableName string) (Store, error) {
	if tableName == "" {
		return nil, errors.New("airdrop store: no table name")
	}
	return &dynamoStore{tn: tableName}, nil
}

func (s *dynamoStore) svc() *dynamodb.DynamoDB {
	// per-call construction mirrors internal/data/dynamodb.go
	return dynamodb.New(session.Must(session.NewSession()))
}

func isConditionalFailure(err error) bool {
	var ae awserr.Error
	if errors.As(err, &ae) {
		return ae.Code() == dynamodb.ErrCodeConditionalCheckFailedException
	}
	return false
}

func (s *dynamoStore) Enqueue(j *Job) error {
	av, err := dynamodbattribute.MarshalMap(*j)
	if err != nil {
		return err
	}
	_, err = s.svc().PutItem(&dynamodb.PutItemInput{
		TableName:           aws.String(s.tn),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(address)"),
	})
	if isConditionalFailure(err) {
		return ErrJobExists
	}
	return err
}

func (s *dynamoStore) Get(address string) (*Job, error) {
	r, err := s.svc().GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(s.tn),
		Key:       map[string]*dynamodb.AttributeValue{"address": {S: aws.String(address)}},
	})
	if err != nil {
		return nil, err
	}
	if r.Item == nil {
		return nil, ErrJobMissing
	}
	j := &Job{}
	if err := dynamodbattribute.UnmarshalMap(r.Item, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *dynamoStore) update(address string, expr string, cond *string, names map[string]*string, values map[string]*dynamodb.AttributeValue) (*Job, error) {
	out, err := s.svc().UpdateItem(&dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.tn),
		Key:                       map[string]*dynamodb.AttributeValue{"address": {S: aws.String(address)}},
		UpdateExpression:          aws.String(expr),
		ConditionExpression:       cond,
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		ReturnValues:              aws.String("ALL_NEW"),
	})
	if err != nil {
		return nil, err
	}
	j := &Job{}
	if err := dynamodbattribute.UnmarshalMap(out.Attributes, j); err != nil {
		return nil, err
	}
	return j, nil
}

func nowMilli() int64 { return time.Now().UnixMilli() }

// Claim atomically transitions an actionable job to processing. Exactly one
// concurrent caller wins; the rest get ErrNotClaimed.
func (s *dynamoStore) Claim(address string, now, leaseUntil int64) (*Job, error) {
	j, err := s.update(address,
		"SET #st = :processing, lease_until = :lease, attempts = attempts + :one, started_at = if_not_exists(started_at, :now), updated_at = :now",
		aws.String("(#st = :pending OR #st = :retryable OR (#st = :processing AND lease_until < :now)) AND next_attempt_at <= :now AND attribute_exists(address)"),
		map[string]*string{"#st": aws.String("status")},
		map[string]*dynamodb.AttributeValue{
			":processing": {S: aws.String(StatusProcessing)},
			":pending":    {S: aws.String(StatusPending)},
			":retryable":  {S: aws.String(StatusFailedRetryable)},
			":lease":      {N: aws.String(itoa(leaseUntil))},
			":now":        {N: aws.String(itoa(now))},
			":one":        {N: aws.String("1")},
		})
	if err != nil && isConditionalFailure(err) {
		return nil, ErrNotClaimed
	}
	return j, err
}

func (s *dynamoStore) Release(address string, nextAttemptAt int64, code, msg string) error {
	_, err := s.update(address,
		"SET #st = :st, next_attempt_at = :next, lease_until = :zero, last_error_code = :code, last_error_msg = :msg, updated_at = :now",
		nil,
		map[string]*string{"#st": aws.String("status")},
		map[string]*dynamodb.AttributeValue{
			":st":   {S: aws.String(StatusFailedRetryable)},
			":next": {N: aws.String(itoa(nextAttemptAt))},
			":zero": {N: aws.String("0")},
			":code": {S: aws.String(code)},
			":msg":  {S: aws.String(msg)},
			":now":  {N: aws.String(itoa(nowMilli()))},
		})
	return err
}

func (s *dynamoStore) MarkProcessingSignature(address, sig string) error {
	_, err := s.update(address,
		"SET signature = :sig, signature_at = :now, updated_at = :now",
		nil, nil,
		map[string]*dynamodb.AttributeValue{
			":sig": {S: aws.String(sig)},
			":now": {N: aws.String(itoa(nowMilli()))},
		})
	return err
}

func (s *dynamoStore) MarkConfirmed(address, sig string) error {
	_, err := s.update(address,
		"SET #st = :st, signature = :sig, confirmed_at = :now, updated_at = :now, lease_until = :zero",
		nil,
		map[string]*string{"#st": aws.String("status")},
		map[string]*dynamodb.AttributeValue{
			":st":   {S: aws.String(StatusConfirmed)},
			":sig":  {S: aws.String(sig)},
			":zero": {N: aws.String("0")},
			":now":  {N: aws.String(itoa(nowMilli()))},
		})
	return err
}

func (s *dynamoStore) MarkTerminal(address, code, msg string) error {
	_, err := s.update(address,
		"SET #st = :st, last_error_code = :code, last_error_msg = :msg, updated_at = :now, lease_until = :zero",
		nil,
		map[string]*string{"#st": aws.String("status")},
		map[string]*dynamodb.AttributeValue{
			":st":   {S: aws.String(StatusFailedTerminal)},
			":code": {S: aws.String(code)},
			":msg":  {S: aws.String(msg)},
			":zero": {N: aws.String("0")},
			":now":  {N: aws.String(itoa(nowMilli()))},
		})
	return err
}

// ResetForRetry re-arms a failed job (operator action).
func (s *dynamoStore) ResetForRetry(address string) error {
	_, err := s.update(address,
		"SET #st = :st, attempts = :zero, next_attempt_at = :zero, lease_until = :zero, last_error_code = :empty, last_error_msg = :empty, updated_at = :now",
		aws.String("#st = :terminal OR #st = :retryable OR (#st = :processing AND lease_until < :now)"),
		map[string]*string{"#st": aws.String("status")},
		map[string]*dynamodb.AttributeValue{
			":st":       {S: aws.String(StatusPending)},
			":terminal": {S: aws.String(StatusFailedTerminal)},
			":retryable": {S: aws.String(StatusFailedRetryable)},
			":processing": {S: aws.String(StatusProcessing)},
			":zero":     {N: aws.String("0")},
			":empty":    {S: aws.String("-")},
			":now":      {N: aws.String(itoa(nowMilli()))},
		})
	if err != nil && isConditionalFailure(err) {
		return ErrNotClaimed
	}
	return err
}

func (s *dynamoStore) scan(filter *string, names map[string]*string, values map[string]*dynamodb.AttributeValue) ([]*Job, error) {
	jobs := []*Job{}
	input := &dynamodb.ScanInput{
		TableName:                 aws.String(s.tn),
		FilterExpression:          filter,
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
	}
	for {
		res, err := s.svc().Scan(input)
		if err != nil {
			return nil, err
		}
		for _, item := range res.Items {
			j := &Job{}
			if err := dynamodbattribute.UnmarshalMap(item, j); err != nil {
				return nil, err
			}
			jobs = append(jobs, j)
		}
		if res.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = res.LastEvaluatedKey
	}
	return jobs, nil
}

// ListActionable returns jobs ready to (re)process: pending/failed_retryable
// past their next attempt time, plus processing rows whose lease expired
// (crash recovery / reconciliation).
func (s *dynamoStore) ListActionable(now int64) ([]*Job, error) {
	return s.scan(
		aws.String("((#st = :pending OR #st = :retryable) AND next_attempt_at <= :now) OR (#st = :processing AND lease_until < :now)"),
		map[string]*string{"#st": aws.String("status")},
		map[string]*dynamodb.AttributeValue{
			":pending":    {S: aws.String(StatusPending)},
			":retryable":  {S: aws.String(StatusFailedRetryable)},
			":processing": {S: aws.String(StatusProcessing)},
			":now":        {N: aws.String(itoa(now))},
		})
}

func (s *dynamoStore) ListAll() ([]*Job, error) {
	return s.scan(nil, nil, nil)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
