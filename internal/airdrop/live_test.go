package airdrop

// Live tests against the real Airdrop_TEST DynamoDB table (convention of
// Waitlist_TEST in internal/data). They skip when AWS credentials cannot be
// resolved, so `go test ./...` stays green offline.

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gagliardetto/solana-go"
)

const liveTableName = "Airdrop_TEST"

func requireAWS(t *testing.T) {
	t.Helper()
	sess, err := session.NewSession()
	if err != nil {
		t.Skipf("no AWS session: %v", err)
	}
	if _, err := sess.Config.Credentials.Get(); err != nil {
		t.Skipf("no AWS credentials: %v", err)
	}
}

func deleteLiveJob(t *testing.T, address string) {
	t.Helper()
	svc := dynamodb.New(session.Must(session.NewSession()))
	_, err := svc.DeleteItem(&dynamodb.DeleteItemInput{
		TableName: aws.String(liveTableName),
		Key:       map[string]*dynamodb.AttributeValue{"address": {S: aws.String(address)}},
	})
	if err != nil {
		t.Fatalf("cleaning up live job %s: %v", address, err)
	}
}

func newLiveJob(address string) *Job {
	now := time.Now().UnixMilli()
	return &Job{
		Address:       address,
		Campaign:      Campaign,
		Cluster:       Cluster,
		Mint:          ExpectedMint.String(),
		Decimals:      Decimals,
		RawAmount:     RawAmount,
		Status:        StatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
		NextAttemptAt: now,
	}
}

func TestLiveStoreEnqueueIdempotency(t *testing.T) {
	requireAWS(t)
	s, err := NewDynamoStore(liveTableName)
	if err != nil {
		t.Fatal(err)
	}
	address := solana.NewWallet().PublicKey().String()
	t.Cleanup(func() { deleteLiveJob(t, address) })

	if err := s.Enqueue(newLiveJob(address)); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if err := s.Enqueue(newLiveJob(address)); !errors.Is(err, ErrJobExists) {
		t.Fatalf("second enqueue: err = %v, want ErrJobExists", err)
	}

	j, err := s.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusPending || j.RawAmount != RawAmount || j.Mint != ExpectedMint.String() {
		t.Errorf("round-trip mismatch: %+v", j)
	}
}

func TestLiveStoreClaimLease(t *testing.T) {
	requireAWS(t)
	s, err := NewDynamoStore(liveTableName)
	if err != nil {
		t.Fatal(err)
	}
	address := solana.NewWallet().PublicKey().String()
	t.Cleanup(func() { deleteLiveJob(t, address) })

	if err := s.Enqueue(newLiveJob(address)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()

	j, err := s.Claim(address, now, now+90_000)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if j.Status != StatusProcessing || j.Attempts != 1 {
		t.Errorf("claimed job: %+v", j)
	}

	// A live lease blocks a second claim.
	if _, err := s.Claim(address, now, now+90_000); !errors.Is(err, ErrNotClaimed) {
		t.Fatalf("second claim: err = %v, want ErrNotClaimed", err)
	}

	// An expired lease is claimable again (crash recovery).
	future := now + 120_000
	if _, err := s.Claim(address, future, future+90_000); err != nil {
		t.Fatalf("expired-lease claim: %v", err)
	}

	// Status transitions round-trip.
	if err := s.MarkProcessingSignature(address, "livesig"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConfirmed(address, "livesig"); err != nil {
		t.Fatal(err)
	}
	j, err = s.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusConfirmed || j.Signature != "livesig" || j.ConfirmedAt == 0 {
		t.Errorf("confirmed job: %+v", j)
	}
	// Confirmed jobs are never actionable nor re-armable.
	if _, err := s.Claim(address, future+200_000, future+300_000); !errors.Is(err, ErrNotClaimed) {
		t.Errorf("confirmed job claim: err = %v, want ErrNotClaimed", err)
	}
	if err := s.ResetForRetry(address); !errors.Is(err, ErrNotClaimed) {
		t.Errorf("confirmed job retry: err = %v, want ErrNotClaimed", err)
	}
}
