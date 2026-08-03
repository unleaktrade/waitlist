package airdrop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"
	ata "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

// Config account layout (anchor, settlement_engine IDL):
// discriminator(8) + admin(32) + usdc_mint(32) + treasury_wallet(32) +
// liquidity_guard(32) + facilitator_fee_bps(2) + bump(1).
var configDiscriminator = []byte{155, 12, 170, 224, 30, 250, 204, 130}

const (
	usdcMintOffset  = 8 + 32 // discriminator + admin
	configMinLength = usdcMintOffset + 32
)

var (
	ErrMintDrift      = errors.New("on-chain config usdcMint does not match expected mint")
	ErrNotConfirmed   = errors.New("transaction not confirmed before deadline")
	ErrTxFailedOnline = errors.New("transaction failed on-chain")
)

// Chain abstracts the Solana RPC operations the worker needs, so tests can
// run against a mock (see mock.go).
type Chain interface {
	// VerifyMint fetches Config.usdcMint and compares it to ExpectedMint;
	// returns ErrMintDrift on mismatch.
	VerifyMint(ctx context.Context) error
	// AuthorityBalances returns the authority's SOL lamports and token
	// balance (raw base units) on its ATA for the expected mint.
	AuthorityBalances(ctx context.Context) (lamports uint64, tokens uint64, err error)
	// SignatureConfirmed reports whether sig reached at least confirmed
	// commitment; onchainErr is non-nil when the tx landed but failed.
	SignatureConfirmed(ctx context.Context, sig solana.Signature) (confirmed bool, onchainErr error, err error)
	// BuildTransfer builds and signs a transaction sending RawAmount to
	// recipient, creating its ATA if absent (idempotent).
	BuildTransfer(ctx context.Context, recipient solana.PublicKey) (*solana.Transaction, solana.Signature, error)
	// Send broadcasts a previously built transaction.
	Send(ctx context.Context, tx *solana.Transaction) (solana.Signature, error)
	// WaitConfirmed blocks until sig is confirmed, fails on-chain
	// (ErrTxFailedOnline) or the deadline passes (ErrNotConfirmed).
	WaitConfirmed(ctx context.Context, sig solana.Signature) error
}

type rpcChain struct {
	client    *rpc.Client
	authority solana.PrivateKey
}

// NewChain builds the production Chain against the given RPC URL. The
// authority private key's public key must match AuthorityPub.
func NewChain(rpcURL string, authority solana.PrivateKey) (Chain, error) {
	if !authority.PublicKey().Equals(AuthorityPub) {
		return nil, fmt.Errorf("authority key mismatch: got %s, want %s", authority.PublicKey(), AuthorityPub)
	}
	return &rpcChain{client: rpc.New(rpcURL), authority: authority}, nil
}

// DecodeConfigUSDCMint extracts usdc_mint from raw Config account data.
// Split out from the RPC fetch for offline testing.
func DecodeConfigUSDCMint(owner solana.PublicKey, data []byte) (solana.PublicKey, error) {
	if !owner.Equals(SettlementPID) {
		return solana.PublicKey{}, fmt.Errorf("config account owned by %s, want %s", owner, SettlementPID)
	}
	if len(data) < configMinLength {
		return solana.PublicKey{}, fmt.Errorf("config account data too short: %d bytes", len(data))
	}
	if !bytes.Equal(data[:8], configDiscriminator) {
		return solana.PublicKey{}, errors.New("config account discriminator mismatch")
	}
	return solana.PublicKeyFromBytes(data[usdcMintOffset : usdcMintOffset+32]), nil
}

func (ch *rpcChain) VerifyMint(ctx context.Context) error {
	res, err := ch.client.GetAccountInfoWithOpts(ctx, ConfigPDA, &rpc.GetAccountInfoOpts{
		Encoding:   solana.EncodingBase64,
		Commitment: rpc.CommitmentConfirmed,
	})
	if err != nil {
		return fmt.Errorf("fetching config account: %w", err)
	}
	if res.Value == nil {
		return errors.New("config account not found")
	}
	mint, err := DecodeConfigUSDCMint(res.Value.Owner, res.Value.Data.GetBinary())
	if err != nil {
		return err
	}
	if !mint.Equals(ExpectedMint) {
		return fmt.Errorf("%w: on-chain %s, expected %s", ErrMintDrift, mint, ExpectedMint)
	}
	return nil
}

func (ch *rpcChain) AuthorityBalances(ctx context.Context) (uint64, uint64, error) {
	balRes, err := ch.client.GetBalance(ctx, AuthorityPub, rpc.CommitmentConfirmed)
	if err != nil {
		return 0, 0, fmt.Errorf("fetching authority SOL balance: %w", err)
	}
	srcATA, _, err := solana.FindAssociatedTokenAddress(AuthorityPub, ExpectedMint)
	if err != nil {
		return 0, 0, err
	}
	tokRes, err := ch.client.GetTokenAccountBalance(ctx, srcATA, rpc.CommitmentConfirmed)
	if err != nil {
		return 0, 0, fmt.Errorf("fetching authority token balance: %w", err)
	}
	var tokens uint64
	if tokRes.Value != nil {
		fmt.Sscan(tokRes.Value.Amount, &tokens)
	}
	return balRes.Value, tokens, nil
}

func (ch *rpcChain) SignatureConfirmed(ctx context.Context, sig solana.Signature) (bool, error, error) {
	res, err := ch.client.GetSignatureStatuses(ctx, true, sig)
	if err != nil {
		return false, nil, err
	}
	if len(res.Value) == 0 || res.Value[0] == nil {
		return false, nil, nil // unknown to the cluster
	}
	st := res.Value[0]
	if st.Err != nil {
		return false, fmt.Errorf("%w: %v", ErrTxFailedOnline, st.Err), nil
	}
	confirmed := st.ConfirmationStatus == rpc.ConfirmationStatusConfirmed ||
		st.ConfirmationStatus == rpc.ConfirmationStatusFinalized
	return confirmed, nil, nil
}

// newCreateIdempotentATAInstruction hand-rolls the CreateIdempotent variant of
// the associated-token-account program: solana-go v1.14.0 only ships Create
// (data []byte{}); CreateIdempotent uses the same six account metas with
// instruction data [1] and no-ops when the ATA already exists.
func newCreateIdempotentATAInstruction(payer, wallet, mint solana.PublicKey) (solana.Instruction, error) {
	ataAddr, _, err := solana.FindAssociatedTokenAddress(wallet, mint)
	if err != nil {
		return nil, err
	}
	return solana.NewInstruction(
		ata.ProgramID,
		solana.AccountMetaSlice{
			solana.NewAccountMeta(payer, true, true),
			solana.NewAccountMeta(ataAddr, true, false),
			solana.NewAccountMeta(wallet, false, false),
			solana.NewAccountMeta(mint, false, false),
			solana.NewAccountMeta(solana.SystemProgramID, false, false),
			solana.NewAccountMeta(solana.TokenProgramID, false, false),
		},
		[]byte{1},
	), nil
}

func (ch *rpcChain) BuildTransfer(ctx context.Context, recipient solana.PublicKey) (*solana.Transaction, solana.Signature, error) {
	srcATA, _, err := solana.FindAssociatedTokenAddress(AuthorityPub, ExpectedMint)
	if err != nil {
		return nil, solana.Signature{}, err
	}
	dstATA, _, err := solana.FindAssociatedTokenAddress(recipient, ExpectedMint)
	if err != nil {
		return nil, solana.Signature{}, err
	}

	createATA, err := newCreateIdempotentATAInstruction(AuthorityPub, recipient, ExpectedMint)
	if err != nil {
		return nil, solana.Signature{}, err
	}
	transfer := token.NewTransferCheckedInstruction(
		RawAmount, Decimals,
		srcATA, ExpectedMint, dstATA, AuthorityPub, nil,
	).Build()

	bh, err := ch.client.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, solana.Signature{}, fmt.Errorf("fetching blockhash: %w", err)
	}

	tx, err := solana.NewTransaction(
		[]solana.Instruction{createATA, transfer},
		bh.Value.Blockhash,
		solana.TransactionPayer(AuthorityPub),
	)
	if err != nil {
		return nil, solana.Signature{}, err
	}
	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(AuthorityPub) {
			return &ch.authority
		}
		return nil
	})
	if err != nil {
		return nil, solana.Signature{}, fmt.Errorf("signing transaction: %w", err)
	}
	return tx, tx.Signatures[0], nil
}

func (ch *rpcChain) Send(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	return ch.client.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{
		PreflightCommitment: rpc.CommitmentConfirmed,
	})
}

func (ch *rpcChain) WaitConfirmed(ctx context.Context, sig solana.Signature) error {
	// ~75s covers blockhash validity (~150 slots).
	deadline := time.Now().Add(75 * time.Second)
	for {
		confirmed, onchainErr, err := ch.SignatureConfirmed(ctx, sig)
		if onchainErr != nil {
			return onchainErr
		}
		if err == nil && confirmed {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrNotConfirmed
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
