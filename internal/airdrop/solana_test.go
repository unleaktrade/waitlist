package airdrop

import (
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"
)

func validConfigData(mint solana.PublicKey) []byte {
	data := make([]byte, 8+32*4+2+1)
	copy(data[:8], configDiscriminator)
	copy(data[8:40], solana.NewWallet().PublicKey().Bytes())    // admin
	copy(data[40:72], mint.Bytes())                             // usdc_mint
	copy(data[72:104], solana.NewWallet().PublicKey().Bytes())  // treasury
	copy(data[104:136], solana.NewWallet().PublicKey().Bytes()) // liquidity guard
	return data
}

func TestDecodeConfigUSDCMint(t *testing.T) {
	tt := []struct {
		name    string
		owner   solana.PublicKey
		data    []byte
		want    solana.PublicKey
		wantErr bool
	}{
		{"valid config", SettlementPID, validConfigData(ExpectedMint), ExpectedMint, false},
		{"drifted mint decodes fine", SettlementPID, validConfigData(AuthorityPub), AuthorityPub, false},
		{"wrong owner", solana.SystemProgramID, validConfigData(ExpectedMint), solana.PublicKey{}, true},
		{"short data", SettlementPID, make([]byte, 40), solana.PublicKey{}, true},
		{"wrong discriminator", SettlementPID, append(make([]byte, 8), validConfigData(ExpectedMint)[8:]...), solana.PublicKey{}, true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeConfigUSDCMint(tc.owner, tc.data)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mint %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equals(tc.want) {
				t.Errorf("mint = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNewCreateIdempotentATAInstruction(t *testing.T) {
	recipient := solana.NewWallet().PublicKey()
	inst, err := newCreateIdempotentATAInstruction(AuthorityPub, recipient, ExpectedMint)
	if err != nil {
		t.Fatal(err)
	}

	data, err := inst.Data()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 || data[0] != 1 {
		t.Errorf("instruction data = %v, want [1] (CreateIdempotent discriminant)", data)
	}

	if !inst.ProgramID().Equals(solana.SPLAssociatedTokenAccountProgramID) {
		t.Errorf("program = %s, want ATA program", inst.ProgramID())
	}

	accounts := inst.Accounts()
	if len(accounts) != 6 {
		t.Fatalf("got %d accounts, want 6", len(accounts))
	}
	ataAddr, _, _ := solana.FindAssociatedTokenAddress(recipient, ExpectedMint)
	want := []struct {
		key      solana.PublicKey
		signer   bool
		writable bool
	}{
		{AuthorityPub, true, true},
		{ataAddr, false, true},
		{recipient, false, false},
		{ExpectedMint, false, false},
		{solana.SystemProgramID, false, false},
		{solana.TokenProgramID, false, false},
	}
	for i, w := range want {
		a := accounts[i]
		if !a.PublicKey.Equals(w.key) {
			t.Errorf("account %d = %s, want %s", i, a.PublicKey, w.key)
		}
		if a.IsSigner != w.signer {
			t.Errorf("account %d signer = %v, want %v", i, a.IsSigner, w.signer)
		}
		if a.IsWritable != w.writable {
			t.Errorf("account %d writable = %v, want %v", i, a.IsWritable, w.writable)
		}
	}
}

func TestNewChainRejectsWrongAuthority(t *testing.T) {
	wrong := solana.NewWallet().PrivateKey
	if _, err := NewChain("http://localhost:0", wrong); err == nil {
		t.Fatal("expected authority mismatch error")
	}
}

func TestResponseFor(t *testing.T) {
	tt := []struct {
		status        string
		wantStatus    string
		wantRetryable bool
	}{
		{StatusPending, StatusPending, true},
		{StatusProcessing, StatusProcessing, true},
		{StatusConfirmed, StatusAlreadyDistributed, false},
		{StatusAlreadyDistributed, StatusAlreadyDistributed, false},
		{StatusFailedRetryable, StatusFailedRetryable, true},
		{StatusFailedTerminal, StatusFailedTerminal, false},
	}
	for _, tc := range tt {
		t.Run(tc.status, func(t *testing.T) {
			r := ResponseFor(&Job{Status: tc.status, Cluster: Cluster, Mint: ExpectedMint.String(), Signature: "sig"})
			if r.Status != tc.wantStatus {
				t.Errorf("status = %s, want %s", r.Status, tc.wantStatus)
			}
			if r.Retryable != tc.wantRetryable {
				t.Errorf("retryable = %v, want %v", r.Retryable, tc.wantRetryable)
			}
			if r.Amount != Amount || r.RawAmount != "1000000000" {
				t.Errorf("amount fields wrong: %+v", r)
			}
		})
	}
}

func TestErrMintDriftIs(t *testing.T) {
	err := errors.Join(ErrMintDrift)
	if !errors.Is(err, ErrMintDrift) {
		t.Fatal("wrapped drift error must match ErrMintDrift")
	}
}
