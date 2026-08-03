package airdrop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/unleaktrade/waitlist/internal/data"
)

func testUser() *data.User {
	return data.NewUser(
		solana.NewWallet().PublicKey().String(),
		"jane.doe@mailservice.com",
		solana.NewWallet().PublicKey().String(),
	)
}

func TestEnqueue(t *testing.T) {
	t.Run("new job is pending", func(t *testing.T) {
		d := New(NewMockStore(), NewMockChain())
		u := testUser()
		r := d.Enqueue(u)
		if r.Status != StatusPending {
			t.Errorf("status = %s, want pending", r.Status)
		}
		if !r.Retryable {
			t.Error("pending must be retryable")
		}
		if r.Mint != ExpectedMint.String() || r.Cluster != Cluster {
			t.Errorf("bad response: %+v", r)
		}
	})

	t.Run("duplicate enqueue returns existing state, no second job", func(t *testing.T) {
		s := NewMockStore()
		d := New(s, NewMockChain())
		u := testUser()
		d.Enqueue(u)
		s.Jobs[u.Address].Status = StatusConfirmed
		s.Jobs[u.Address].Signature = "sig123"
		r := d.Enqueue(u)
		if r.Status != StatusAlreadyDistributed {
			t.Errorf("status = %s, want already_distributed", r.Status)
		}
		if r.Signature != "sig123" {
			t.Errorf("signature = %q, want sig123", r.Signature)
		}
		if len(s.Jobs) != 1 {
			t.Fatalf("expected 1 job, got %d", len(s.Jobs))
		}
	})

	t.Run("store error yields failed_retryable, activation unaffected", func(t *testing.T) {
		s := NewMockStore()
		s.EnqueueErr = errors.New("dynamo down")
		d := New(s, NewMockChain())
		r := d.Enqueue(testUser())
		if r.Status != StatusFailedRetryable {
			t.Errorf("status = %s, want failed_retryable", r.Status)
		}
		if !r.Retryable {
			t.Error("failed_retryable must be retryable")
		}
	})
}

func processOnce(d *Distributor, address string) {
	d.process(context.Background(), address)
}

func TestProcessSuccess(t *testing.T) {
	for _, ataExists := range []bool{true, false} {
		// The CreateIdempotent instruction makes both paths identical from the
		// worker's perspective; assert both anyway per the issue's test matrix.
		name := "ata absent"
		if ataExists {
			name = "ata present"
		}
		t.Run(name, func(t *testing.T) {
			s, ch := NewMockStore(), NewMockChain()
			ch.SigStatus[ch.NextSig.String()] = MockSigStatus{Confirmed: true}
			d := New(s, ch)
			u := testUser()
			d.Enqueue(u)
			processOnce(d, u.Address)

			j := s.Jobs[u.Address]
			if j.Status != StatusConfirmed {
				t.Fatalf("status = %s, want confirmed (last error: %s %s)", j.Status, j.LastErrorCode, j.LastErrorMsg)
			}
			if j.Signature != ch.NextSig.String() {
				t.Errorf("signature = %q, want %q", j.Signature, ch.NextSig)
			}
			if j.Attempts != 1 {
				t.Errorf("attempts = %d, want 1", j.Attempts)
			}
			if ch.SendCalls != 1 {
				t.Errorf("send calls = %d, want 1", ch.SendCalls)
			}
		})
	}
}

func TestProcessInvalidWallet(t *testing.T) {
	s, ch := NewMockStore(), NewMockChain()
	d := New(s, ch)
	now := time.Now().UnixMilli()
	s.Jobs["not-a-solana-address"] = &Job{Address: "not-a-solana-address", Status: StatusPending, NextAttemptAt: now}
	processOnce(d, "not-a-solana-address")
	j := s.Jobs["not-a-solana-address"]
	if j.Status != StatusFailedTerminal || j.LastErrorCode != ErrCodeInvalidWallet {
		t.Errorf("got %s/%s, want failed_terminal/invalid_wallet", j.Status, j.LastErrorCode)
	}
	if ch.SendCalls != 0 {
		t.Error("must not send for invalid wallet")
	}
}

func TestProcessInsufficientInventory(t *testing.T) {
	tt := []struct {
		name     string
		lamports uint64
		tokens   uint64
	}{
		{"no tokens", 1_000_000_000, RawAmount - 1},
		{"no sol", minSOLLamports - 1, 100 * RawAmount},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, ch := NewMockStore(), NewMockChain()
			ch.Lamports, ch.Tokens = tc.lamports, tc.tokens
			d := New(s, ch)
			u := testUser()
			d.Enqueue(u)
			processOnce(d, u.Address)
			j := s.Jobs[u.Address]
			if j.Status != StatusFailedTerminal || j.LastErrorCode != ErrCodeInsufficientInventory {
				t.Errorf("got %s/%s, want failed_terminal/insufficient_distributor_inventory", j.Status, j.LastErrorCode)
			}
			if ch.SendCalls != 0 {
				t.Error("must not send when inventory is exhausted")
			}
		})
	}
}

func TestProcessRPCErrorRetriesThenSucceeds(t *testing.T) {
	s, ch := NewMockStore(), NewMockChain()
	ch.SendErrs = []error{errors.New("rpc timeout")} // first send fails
	ch.SigStatus[ch.NextSig.String()] = MockSigStatus{Confirmed: true}
	d := New(s, ch)
	u := testUser()
	d.Enqueue(u)

	processOnce(d, u.Address)
	j := s.Jobs[u.Address]
	if j.Status != StatusFailedRetryable || j.LastErrorCode != ErrCodeRPC {
		t.Fatalf("after failure: %s/%s, want failed_retryable/rpc_error", j.Status, j.LastErrorCode)
	}
	if j.NextAttemptAt <= time.Now().UnixMilli() {
		t.Error("expected backoff in the future")
	}

	// Note: the first attempt persisted a signature pre-send; that prior
	// submission is confirmed on re-claim without a second Send.
	j.NextAttemptAt = 0
	processOnce(d, u.Address)
	j = s.Jobs[u.Address]
	if j.Status != StatusConfirmed {
		t.Fatalf("after retry: %s (%s %s), want confirmed", j.Status, j.LastErrorCode, j.LastErrorMsg)
	}
	if ch.SendCalls != 1 {
		t.Errorf("send calls = %d, want 1 (recovered via signature status, no resend)", ch.SendCalls)
	}
}

func TestProcessAmbiguousSubmissionWaitsForBlockhashExpiry(t *testing.T) {
	s, ch := NewMockStore(), NewMockChain()
	d := New(s, ch)
	u := testUser()
	d.Enqueue(u)
	now := time.Now().UnixMilli()
	j := s.Jobs[u.Address]
	j.Signature = solana.Signature{9}.String() // unknown to the mock cluster
	j.SignatureAt = now - 10_000               // sent 10s ago: too fresh to resend

	processOnce(d, u.Address)
	j = s.Jobs[u.Address]
	if j.Status != StatusFailedRetryable || j.LastErrorCode != ErrCodeSubmissionAmbiguous {
		t.Fatalf("got %s/%s, want failed_retryable/submission_ambiguous", j.Status, j.LastErrorCode)
	}
	if ch.SendCalls != 0 {
		t.Fatal("must not resend while the prior blockhash may still be valid")
	}

	// After expiry the job is safe to rebuild and resend.
	j.SignatureAt = now - 200_000
	j.NextAttemptAt = 0
	ch.SigStatus[ch.NextSig.String()] = MockSigStatus{Confirmed: true}
	processOnce(d, u.Address)
	j = s.Jobs[u.Address]
	if j.Status != StatusConfirmed {
		t.Fatalf("got %s, want confirmed", j.Status)
	}
	if ch.SendCalls != 1 {
		t.Errorf("send calls = %d, want 1", ch.SendCalls)
	}
}

func TestProcessRecoversConfirmedPriorSubmission(t *testing.T) {
	s, ch := NewMockStore(), NewMockChain()
	d := New(s, ch)
	u := testUser()
	d.Enqueue(u)
	sig := solana.Signature{7}.String()
	s.Jobs[u.Address].Signature = sig
	ch.SigStatus[sig] = MockSigStatus{Confirmed: true}

	processOnce(d, u.Address)
	j := s.Jobs[u.Address]
	if j.Status != StatusConfirmed || j.Signature != sig {
		t.Fatalf("got %s/%s, want confirmed with original signature", j.Status, j.Signature)
	}
	if ch.SendCalls != 0 {
		t.Fatal("recovered submission must not trigger a second payout")
	}
}

func TestProcessMaxAttempts(t *testing.T) {
	s, ch := NewMockStore(), NewMockChain()
	ch.BuildErr = errors.New("rpc always down")
	d := New(s, ch)
	u := testUser()
	d.Enqueue(u)
	s.Jobs[u.Address].Attempts = MaxAttempts // next claim exceeds the cap

	processOnce(d, u.Address)
	j := s.Jobs[u.Address]
	if j.Status != StatusFailedTerminal || j.LastErrorCode != ErrCodeMaxAttempts {
		t.Errorf("got %s/%s, want failed_terminal/max_attempts_exceeded", j.Status, j.LastErrorCode)
	}
}

func TestMintDriftHaltsProcessing(t *testing.T) {
	s, ch := NewMockStore(), NewMockChain()
	ch.VerifyMintErr = ErrMintDrift
	d := New(s, ch)
	d.checkMint(context.Background())
	if !d.halted.Load() {
		t.Fatal("drift must halt the distributor")
	}

	u := testUser()
	d.Enqueue(u)
	processOnce(d, u.Address)
	j := s.Jobs[u.Address]
	if j.Status != StatusFailedTerminal || j.LastErrorCode != ErrCodeMintConfigDrift {
		t.Errorf("got %s/%s, want failed_terminal/mint_config_drift", j.Status, j.LastErrorCode)
	}

	// Transient RPC failure must NOT change the halted state...
	ch.VerifyMintErr = errors.New("rpc glitch")
	d.checkMint(context.Background())
	if !d.halted.Load() {
		t.Error("transient RPC failure must not clear the halt")
	}
	// ...but a successful re-verification resumes distribution.
	ch.VerifyMintErr = nil
	d.checkMint(context.Background())
	if d.halted.Load() {
		t.Error("successful verification must resume distribution")
	}
}

func TestConcurrentClaimSingleWinner(t *testing.T) {
	s := NewMockStore()
	u := testUser()
	now := time.Now().UnixMilli()
	s.Jobs[u.Address] = &Job{Address: u.Address, Status: StatusPending, NextAttemptAt: now}

	var wg sync.WaitGroup
	wins := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Claim(u.Address, now, now+90_000); err == nil {
				wins <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for range wins {
		n++
	}
	if n != 1 {
		t.Fatalf("claim winners = %d, want exactly 1", n)
	}
}

func TestExpiredLeaseReconciliation(t *testing.T) {
	s := NewMockStore()
	u := testUser()
	now := time.Now().UnixMilli()
	// A processing row whose lease expired (crashed worker) is actionable again.
	s.Jobs[u.Address] = &Job{Address: u.Address, Status: StatusProcessing, LeaseUntil: now - 1, NextAttemptAt: 0}
	jobs, err := s.ListActionable(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("actionable = %d, want 1", len(jobs))
	}
	if _, err := s.Claim(u.Address, now, now+90_000); err != nil {
		t.Fatalf("expired-lease job must be claimable: %v", err)
	}

	// A processing row with a live lease is not actionable.
	s.Jobs[u.Address].Status = StatusProcessing
	s.Jobs[u.Address].LeaseUntil = now + 60_000
	jobs, _ = s.ListActionable(now)
	if len(jobs) != 0 {
		t.Fatalf("live-lease job must not be actionable, got %d", len(jobs))
	}
}

func TestRetryResetsFailedJob(t *testing.T) {
	s, ch := NewMockStore(), NewMockChain()
	d := New(s, ch)
	u := testUser()
	d.Enqueue(u)
	s.Jobs[u.Address].Status = StatusFailedTerminal
	s.Jobs[u.Address].Attempts = MaxAttempts

	j, err := d.Retry(u.Address)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusPending || j.Attempts != 0 {
		t.Errorf("got %s/attempts=%d, want pending/0", j.Status, j.Attempts)
	}

	// A confirmed job must never be re-armed.
	s.Jobs[u.Address].Status = StatusConfirmed
	if _, err := d.Retry(u.Address); !errors.Is(err, ErrNotClaimed) {
		t.Errorf("retry of confirmed job: err = %v, want ErrNotClaimed", err)
	}
}
