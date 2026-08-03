package airdrop

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/unleaktrade/waitlist/internal/data"
)

const (
	leaseDuration    = 90 * time.Second
	pollInterval     = 30 * time.Second
	mintScanInterval = 10 * time.Minute

	// Alert thresholds for the distribution authority.
	minSOLLamports  = uint64(5_000_000)   // 0.005 SOL: below this, payouts terminal
	warnSOLLamports = uint64(50_000_000)  // 0.05 SOL
	warnTokenUnits  = 20 * RawAmount      // 20 payouts left
)

// Distributor owns the distribution queue: it enqueues jobs at activation
// time and processes them in a single background goroutine.
type Distributor struct {
	store  Store
	chain  Chain
	halted atomic.Bool // mint drift: stop paying out, keep queueing
	wake   chan struct{}
}

func New(store Store, chain Chain) *Distributor {
	return &Distributor{
		store: store,
		chain: chain,
		wake:  make(chan struct{}, 64),
	}
}

// Enqueue creates the distribution job for a freshly activated user and
// returns the airdrop object for the activation response. Duplicate calls
// never create a second payout (conditional put).
func (d *Distributor) Enqueue(u *data.User) Response {
	now := time.Now().UnixMilli()
	j := &Job{
		Address:       u.Address,
		WaitlistUUID:  u.UUID,
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
	err := d.store.Enqueue(j)
	switch {
	case err == nil:
		d.poke()
		return NewResponse(StatusPending)
	case errors.Is(err, ErrJobExists):
		if existing, gerr := d.store.Get(u.Address); gerr == nil {
			return ResponseFor(existing)
		}
		return NewResponse(StatusPending)
	default:
		log.Printf("🚨 airdrop: enqueue failed for %s: %v", u.Address, err)
		return NewResponse(StatusFailedRetryable)
	}
}

func (d *Distributor) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run is the worker loop; call it in one goroutine. It returns when ctx is
// cancelled. Restart recovery is implicit: expired-lease processing rows are
// picked up by ListActionable.
func (d *Distributor) Run(ctx context.Context) {
	log.Printf("🪂 airdrop: worker starting (authority %s, mint %s)", AuthorityPub, ExpectedMint)
	d.checkMint(ctx)
	d.logBalances(ctx)
	d.cycle(ctx)

	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	mintScan := time.NewTicker(mintScanInterval)
	defer mintScan.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🪂 airdrop: worker stopped")
			return
		case <-d.wake:
			d.cycle(ctx)
		case <-poll.C:
			d.cycle(ctx)
		case <-mintScan.C:
			d.checkMint(ctx)
		}
	}
}

// checkMint verifies Config.usdcMint; drift halts payouts. A transient RPC
// failure keeps the previous halted state.
func (d *Distributor) checkMint(ctx context.Context) {
	err := d.chain.VerifyMint(ctx)
	switch {
	case err == nil:
		if d.halted.Swap(false) {
			log.Println("🪂 airdrop: mint verified again, resuming distribution")
		}
	case errors.Is(err, ErrMintDrift):
		if !d.halted.Swap(true) {
			log.Printf("🚨 airdrop: MINT CONFIG DRIFT — distribution halted: %v", err)
		}
	default:
		log.Printf("⚠️ airdrop: mint verification failed (transient, keeping state): %v", err)
	}
}

func (d *Distributor) logBalances(ctx context.Context) {
	lamports, tokens, err := d.chain.AuthorityBalances(ctx)
	if err != nil {
		log.Printf("⚠️ airdrop: cannot read authority balances: %v", err)
		return
	}
	payouts := tokens / RawAmount
	log.Printf("🪂 airdrop: authority holds %d lamports and %d token units (%d payouts left)", lamports, tokens, payouts)
	if lamports < warnSOLLamports {
		log.Printf("🚨 airdrop: authority SOL low (%d lamports < %d) — top up soon", lamports, warnSOLLamports)
	}
	if tokens < warnTokenUnits {
		log.Printf("🚨 airdrop: authority token inventory low (%d payouts left) — top up via SPL transfer (minting is impossible)", payouts)
	}
}

func (d *Distributor) cycle(ctx context.Context) {
	now := time.Now().UnixMilli()
	jobs, err := d.store.ListActionable(now)
	if err != nil {
		log.Printf("⚠️ airdrop: listing actionable jobs failed: %v", err)
		return
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		d.process(ctx, j.Address)
	}
}

func (d *Distributor) process(ctx context.Context, address string) {
	now := time.Now().UnixMilli()
	j, err := d.store.Claim(address, now, now+leaseDuration.Milliseconds())
	if errors.Is(err, ErrNotClaimed) {
		return // another worker/cycle won
	}
	if err != nil {
		log.Printf("⚠️ airdrop: claim failed for %s: %v", address, err)
		return
	}

	// Recover a previously ambiguous submission before anything else: if the
	// stored signature already landed, never send a second transfer.
	if j.Signature != "" {
		sig, serr := solana.SignatureFromBase58(j.Signature)
		if serr == nil {
			confirmed, onchainErr, cerr := d.chain.SignatureConfirmed(ctx, sig)
			switch {
			case cerr != nil:
				d.releaseRetry(j, ErrCodeSubmissionAmbiguous, cerr.Error())
				return
			case confirmed:
				if err := d.store.MarkConfirmed(j.Address, j.Signature); err != nil {
					log.Printf("⚠️ airdrop: mark confirmed failed for %s: %v", j.Address, err)
				} else {
					log.Printf("✅ airdrop: %s confirmed (recovered prior submission %s)", j.Address, j.Signature)
				}
				return
			case onchainErr != nil:
				// Landed but failed on-chain: safe to rebuild and resend.
				log.Printf("⚠️ airdrop: prior tx for %s failed on-chain (%v), retrying", j.Address, onchainErr)
			default:
				// Unknown to the cluster: resending is only safe once the old
				// blockhash has certainly expired (~60-90s of validity).
				if now-j.SignatureAt < 120_000 {
					d.releaseRetry(j, ErrCodeSubmissionAmbiguous, "prior submission not yet visible; waiting for blockhash expiry")
					return
				}
			}
		}
	}

	if pub, perr := solana.PublicKeyFromBase58(j.Address); perr != nil || !solana.IsOnCurve(pub[:]) {
		d.markTerminal(j, ErrCodeInvalidWallet, "stored wallet is not a valid on-curve Solana public key")
		return
	}

	if d.halted.Load() {
		d.markTerminal(j, ErrCodeMintConfigDrift, "on-chain config usdcMint no longer matches the configured mint")
		return
	}

	lamports, tokens, err := d.chain.AuthorityBalances(ctx)
	if err != nil {
		d.releaseRetry(j, ErrCodeRPC, err.Error())
		return
	}
	if tokens < RawAmount || lamports < minSOLLamports {
		log.Printf("🚨 airdrop: distributor inventory exhausted (lamports=%d tokens=%d) — operator top-up required", lamports, tokens)
		d.markTerminal(j, ErrCodeInsufficientInventory, "distribution authority lacks tokens or SOL; operator top-up required")
		return
	}

	recipient := solana.MustPublicKeyFromBase58(j.Address)
	tx, sig, err := d.chain.BuildTransfer(ctx, recipient)
	if err != nil {
		d.releaseRetry(j, ErrCodeRPC, err.Error())
		return
	}

	// Persist the signature before broadcasting so a crash between Send and
	// MarkConfirmed is recoverable without a double payout.
	if err := d.store.MarkProcessingSignature(j.Address, sig.String()); err != nil {
		d.releaseRetry(j, ErrCodeRPC, err.Error())
		return
	}
	if _, err := d.chain.Send(ctx, tx); err != nil {
		d.releaseRetry(j, classifySendError(err), err.Error())
		return
	}
	if err := d.chain.WaitConfirmed(ctx, sig); err != nil {
		switch {
		case errors.Is(err, ErrTxFailedOnline):
			d.releaseRetry(j, ErrCodeRPC, err.Error())
		case errors.Is(err, ErrNotConfirmed):
			d.releaseRetry(j, ErrCodeConfirmationTimeout, err.Error())
		default:
			d.releaseRetry(j, ErrCodeRPC, err.Error())
		}
		return
	}
	if err := d.store.MarkConfirmed(j.Address, sig.String()); err != nil {
		log.Printf("⚠️ airdrop: payout confirmed but persisting state failed for %s: %v", j.Address, err)
		return
	}
	log.Printf("✅ airdrop: sent %s tokens to %s (tx %s)", Amount, j.Address, sig)
}

func classifySendError(err error) string {
	if strings.Contains(strings.ToLower(err.Error()), "blockhash") {
		return ErrCodeBlockhashExpired
	}
	return ErrCodeRPC
}

func (d *Distributor) releaseRetry(j *Job, code, msg string) {
	if j.Attempts >= MaxAttempts {
		d.markTerminal(j, ErrCodeMaxAttempts, "exceeded max delivery attempts; last error: "+msg)
		return
	}
	backoff := 30 * time.Second << uint(j.Attempts-1)
	if backoff > 15*time.Minute || backoff <= 0 {
		backoff = 15 * time.Minute
	}
	next := time.Now().Add(backoff).UnixMilli()
	if err := d.store.Release(j.Address, next, code, msg); err != nil {
		log.Printf("⚠️ airdrop: release failed for %s: %v", j.Address, err)
		return
	}
	log.Printf("🔁 airdrop: %s attempt %d failed (%s), retrying in %s", j.Address, j.Attempts, code, backoff)
}

func (d *Distributor) markTerminal(j *Job, code, msg string) {
	if err := d.store.MarkTerminal(j.Address, code, msg); err != nil {
		log.Printf("⚠️ airdrop: mark terminal failed for %s: %v", j.Address, err)
		return
	}
	log.Printf("⛔ airdrop: %s failed terminally (%s): %s", j.Address, code, msg)
}

// Retry re-arms a job for the operator endpoint; missing jobs for an
// activated address can be re-created by the caller via Enqueue.
func (d *Distributor) Retry(address string) (*Job, error) {
	if err := d.store.ResetForRetry(address); err != nil {
		return nil, err
	}
	d.poke()
	return d.store.Get(address)
}

// Jobs lists every stored job (operator reconciliation).
func (d *Distributor) Jobs() ([]*Job, error) {
	return d.store.ListAll()
}

// Status returns the stored job for an address.
func (d *Distributor) Status(address string) (*Job, error) {
	return d.store.Get(address)
}
