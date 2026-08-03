// Package airdrop distributes the custom devnet USDC token to activated
// waitlist wallets. The mint cannot be minted against (its supply is already
// u64::MAX), so every payout is a TransferChecked from the pre-funded
// distribution authority. See https://github.com/unleaktrade/waitlist/issues/18
package airdrop

import (
	"github.com/gagliardetto/solana-go"
)

const (
	Cluster  = "devnet"
	Campaign = "devnet-usdc-v1"

	Decimals  = uint8(6)
	RawAmount = uint64(1_000_000_000) // 1000 tokens
	Amount    = "1000"

	// MaxAttempts bounds the retry loop; beyond it a job turns terminal.
	MaxAttempts = 10
)

var (
	// ExpectedMint is verified against the beta app's on-chain Config.usdcMint
	// at startup and on a schedule; drift halts distribution.
	ExpectedMint  = solana.MustPublicKeyFromBase58("5jBqJmY2mKetudVa2XaC8U6UN2BNNirDiTnDEuA6pdyR")
	ConfigPDA     = solana.MustPublicKeyFromBase58("GgE8H46in1MLh8EJs6Je7ps7wEysiEks7ZcMVVgq4xTZ")
	SettlementPID = solana.MustPublicKeyFromBase58("7wrjbU1NbVtUCUGP1obi3aiT6QrjXZnH5XJDXMsKtkPG")
	AuthorityPub  = solana.MustPublicKeyFromBase58("7QVKMkpbpniPhDu3V3iXTauDwkUfaAiSZ9QrqfQioEDy")
)

// Job statuses, exposed verbatim in the activation response.
const (
	StatusPending            = "pending"
	StatusProcessing         = "processing"
	StatusConfirmed          = "confirmed"
	StatusAlreadyDistributed = "already_distributed"
	StatusFailedRetryable    = "failed_retryable"
	StatusFailedTerminal     = "failed_terminal"
)

// Terminal error codes.
const (
	ErrCodeInsufficientInventory = "insufficient_distributor_inventory"
	ErrCodeMintConfigDrift       = "mint_config_drift"
	ErrCodeInvalidWallet         = "invalid_wallet"
	ErrCodeMaxAttempts           = "max_attempts_exceeded"
)

// Retryable error codes.
const (
	ErrCodeRPC                 = "rpc_error"
	ErrCodeBlockhashExpired    = "blockhash_expired"
	ErrCodeConfirmationTimeout = "confirmation_timeout"
	ErrCodeSubmissionAmbiguous = "submission_ambiguous"
)

// Job is one distribution attempt record; the wallet address is the
// uniqueness boundary (one payout per activated wallet per campaign/table).
type Job struct {
	Address       string `dynamodbav:"address" json:"address"`
	WaitlistUUID  string `dynamodbav:"waitlist_uuid" json:"waitlist_uuid"`
	Campaign      string `dynamodbav:"campaign" json:"campaign"`
	Cluster       string `dynamodbav:"cluster" json:"cluster"`
	Mint          string `dynamodbav:"mint" json:"mint"`
	Decimals      uint8  `dynamodbav:"decimals" json:"decimals"`
	RawAmount     uint64 `dynamodbav:"raw_amount" json:"raw_amount"`
	Status        string `dynamodbav:"status" json:"status"`
	Attempts      int    `dynamodbav:"attempts" json:"attempts"`
	Signature     string `dynamodbav:"signature,omitempty" json:"signature,omitempty"`
	SignatureAt   int64  `dynamodbav:"signature_at,omitempty" json:"signature_at,omitempty"`
	CreatedAt     int64  `dynamodbav:"created_at" json:"created_at"`
	StartedAt     int64  `dynamodbav:"started_at,omitempty" json:"started_at,omitempty"`
	ConfirmedAt   int64  `dynamodbav:"confirmed_at,omitempty" json:"confirmed_at,omitempty"`
	UpdatedAt     int64  `dynamodbav:"updated_at" json:"updated_at"`
	NextAttemptAt int64  `dynamodbav:"next_attempt_at" json:"next_attempt_at"`
	LeaseUntil    int64  `dynamodbav:"lease_until,omitempty" json:"lease_until,omitempty"`
	LastErrorCode string `dynamodbav:"last_error_code,omitempty" json:"last_error_code,omitempty"`
	LastErrorMsg  string `dynamodbav:"last_error_msg,omitempty" json:"last_error_msg,omitempty"`
}

// Response is the airdrop object embedded in the activation 201 response.
type Response struct {
	Cluster   string `json:"cluster"`
	Status    string `json:"status"`
	Mint      string `json:"mint"`
	Amount    string `json:"amount"`
	RawAmount string `json:"rawAmount"`
	Signature string `json:"signature,omitempty"`
	Retryable bool   `json:"retryable"`
}

// ResponseFor maps a stored job to the public contract. A confirmed job is
// reported as already_distributed because the caller reaching this path is,
// by definition, not the request that triggered the payout.
func ResponseFor(j *Job) Response {
	status := j.Status
	if status == StatusConfirmed {
		status = StatusAlreadyDistributed
	}
	return Response{
		Cluster:   j.Cluster,
		Status:    status,
		Mint:      j.Mint,
		Amount:    Amount,
		RawAmount: "1000000000",
		Signature: j.Signature,
		Retryable: retryable(status),
	}
}

// NewResponse is the airdrop object for a freshly enqueued job.
func NewResponse(status string) Response {
	return Response{
		Cluster:   Cluster,
		Status:    status,
		Mint:      ExpectedMint.String(),
		Amount:    Amount,
		RawAmount: "1000000000",
		Retryable: retryable(status),
	}
}

func retryable(status string) bool {
	switch status {
	case StatusPending, StatusProcessing, StatusFailedRetryable:
		return true
	default:
		return false
	}
}
