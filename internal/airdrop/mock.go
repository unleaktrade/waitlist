package airdrop

import (
	"context"
	"sync"

	"github.com/gagliardetto/solana-go"
)

// MOCKS — hand-written, following the internal/data/db.go style, exported so
// cmd/api tests can build an App with a scripted Distributor.

// MockStore is an in-memory Store with DynamoDB's conditional semantics.
type MockStore struct {
	mu   sync.Mutex
	Jobs map[string]*Job

	EnqueueErr error
	ClaimErr   error
	GetErr     error
	ListErr    error
}

func NewMockStore() *MockStore {
	return &MockStore{Jobs: map[string]*Job{}}
}

func (s *MockStore) Enqueue(j *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.EnqueueErr != nil {
		return s.EnqueueErr
	}
	if _, ok := s.Jobs[j.Address]; ok {
		return ErrJobExists
	}
	cp := *j
	s.Jobs[j.Address] = &cp
	return nil
}

func (s *MockStore) Get(address string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.GetErr != nil {
		return nil, s.GetErr
	}
	j, ok := s.Jobs[address]
	if !ok {
		return nil, ErrJobMissing
	}
	cp := *j
	return &cp, nil
}

func (s *MockStore) Claim(address string, now, leaseUntil int64) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ClaimErr != nil {
		return nil, s.ClaimErr
	}
	j, ok := s.Jobs[address]
	if !ok {
		return nil, ErrNotClaimed
	}
	claimable := (j.Status == StatusPending || j.Status == StatusFailedRetryable ||
		(j.Status == StatusProcessing && j.LeaseUntil < now)) && j.NextAttemptAt <= now
	if !claimable {
		return nil, ErrNotClaimed
	}
	j.Status = StatusProcessing
	j.LeaseUntil = leaseUntil
	j.Attempts++
	if j.StartedAt == 0 {
		j.StartedAt = now
	}
	j.UpdatedAt = now
	cp := *j
	return &cp, nil
}

func (s *MockStore) Release(address string, nextAttemptAt int64, code, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.Jobs[address]
	if !ok {
		return ErrJobMissing
	}
	j.Status = StatusFailedRetryable
	j.NextAttemptAt = nextAttemptAt
	j.LeaseUntil = 0
	j.LastErrorCode = code
	j.LastErrorMsg = msg
	return nil
}

func (s *MockStore) MarkProcessingSignature(address, sig string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.Jobs[address]
	if !ok {
		return ErrJobMissing
	}
	j.Signature = sig
	j.SignatureAt = nowMilli()
	return nil
}

func (s *MockStore) MarkConfirmed(address, sig string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.Jobs[address]
	if !ok {
		return ErrJobMissing
	}
	j.Status = StatusConfirmed
	j.Signature = sig
	j.ConfirmedAt = nowMilli()
	j.LeaseUntil = 0
	return nil
}

func (s *MockStore) MarkTerminal(address, code, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.Jobs[address]
	if !ok {
		return ErrJobMissing
	}
	j.Status = StatusFailedTerminal
	j.LastErrorCode = code
	j.LastErrorMsg = msg
	j.LeaseUntil = 0
	return nil
}

func (s *MockStore) ResetForRetry(address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.Jobs[address]
	if !ok {
		return ErrJobMissing
	}
	if j.Status != StatusFailedTerminal && j.Status != StatusFailedRetryable &&
		!(j.Status == StatusProcessing && j.LeaseUntil < nowMilli()) {
		return ErrNotClaimed
	}
	j.Status = StatusPending
	j.Attempts = 0
	j.NextAttemptAt = 0
	j.LeaseUntil = 0
	j.LastErrorCode = ""
	j.LastErrorMsg = ""
	return nil
}

func (s *MockStore) ListActionable(now int64) ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ListErr != nil {
		return nil, s.ListErr
	}
	jobs := []*Job{}
	for _, j := range s.Jobs {
		if ((j.Status == StatusPending || j.Status == StatusFailedRetryable) && j.NextAttemptAt <= now) ||
			(j.Status == StatusProcessing && j.LeaseUntil < now) {
			cp := *j
			jobs = append(jobs, &cp)
		}
	}
	return jobs, nil
}

func (s *MockStore) ListAll() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ListErr != nil {
		return nil, s.ListErr
	}
	jobs := []*Job{}
	for _, j := range s.Jobs {
		cp := *j
		jobs = append(jobs, &cp)
	}
	return jobs, nil
}

// MockChain is a scriptable Chain.
type MockChain struct {
	mu sync.Mutex

	VerifyMintErr error
	Lamports      uint64
	Tokens        uint64
	BalancesErr   error

	// SigStatus maps signature → (confirmed, onchainErr); missing = unknown.
	SigStatus map[string]MockSigStatus
	SigErr    error

	BuildErr  error
	SendErrs  []error // consumed one per Send call; nil entry = success
	SendCalls int
	WaitErr   error

	NextSig solana.Signature
}

type MockSigStatus struct {
	Confirmed bool
	OnchainErr error
}

func NewMockChain() *MockChain {
	return &MockChain{
		Lamports:  1_000_000_000,
		Tokens:    100 * RawAmount,
		SigStatus: map[string]MockSigStatus{},
		NextSig:   solana.Signature{1, 2, 3},
	}
}

func (c *MockChain) VerifyMint(ctx context.Context) error { return c.VerifyMintErr }

func (c *MockChain) AuthorityBalances(ctx context.Context) (uint64, uint64, error) {
	return c.Lamports, c.Tokens, c.BalancesErr
}

func (c *MockChain) SignatureConfirmed(ctx context.Context, sig solana.Signature) (bool, error, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.SigErr != nil {
		return false, nil, c.SigErr
	}
	st, ok := c.SigStatus[sig.String()]
	if !ok {
		return false, nil, nil
	}
	return st.Confirmed, st.OnchainErr, nil
}

func (c *MockChain) BuildTransfer(ctx context.Context, recipient solana.PublicKey) (*solana.Transaction, solana.Signature, error) {
	if c.BuildErr != nil {
		return nil, solana.Signature{}, c.BuildErr
	}
	return &solana.Transaction{}, c.NextSig, nil
}

func (c *MockChain) Send(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if c.SendCalls < len(c.SendErrs) {
		err = c.SendErrs[c.SendCalls]
	}
	c.SendCalls++
	if err != nil {
		return solana.Signature{}, err
	}
	return c.NextSig, nil
}

func (c *MockChain) WaitConfirmed(ctx context.Context, sig solana.Signature) error {
	return c.WaitErr
}
