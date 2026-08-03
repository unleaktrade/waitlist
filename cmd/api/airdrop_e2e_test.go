package main

// End-to-end proof that an activated waitlist member's wallet actually
// receives Unleak USDC (uUSDC) on devnet: real Airdrop_TEST DynamoDB table,
// real devnet RPC, real TransferChecked signed by the distribution authority.
//
// Skips unless both AWS credentials and UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY
// are available. Each run deletes the recipient's job row first, so a genuine
// 1000 uUSDC transfer happens every time (~5k lamports fee; the recipient ATA
// rent was paid once, on the first ever run).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	awssession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/unleaktrade/waitlist/internal/airdrop"
	"github.com/unleaktrade/waitlist/internal/cache"
	"github.com/unleaktrade/waitlist/internal/crypto"
	"github.com/unleaktrade/waitlist/internal/crypto/cipher"
	"github.com/unleaktrade/waitlist/internal/data"
	"github.com/unleaktrade/waitlist/internal/limiter"
	"github.com/unleaktrade/waitlist/internal/mailer"
)

const (
	e2eTableName        = "Airdrop_TEST"
	e2eDefaultRecipient = "8GAt381fturbi53tXBKubeKgXAdjKvu4fV7H9sn3z4pZ" // devnet fixture wallet (taker1)
	e2eDefaultRPC       = "https://api.devnet.solana.com"
)

func e2eRecipient() string {
	if r := os.Getenv("UNLEAKTRADE_AIRDROP_TEST_RECIPIENT"); r != "" {
		return r
	}
	return e2eDefaultRecipient
}

func e2eRPCURL() string {
	if u := os.Getenv("UNLEAKTRADE_SOLANA_RPC_URL"); u != "" {
		return u
	}
	return e2eDefaultRPC
}

func requireE2E(t *testing.T) solana.PrivateKey {
	t.Helper()
	sess, err := awssession.NewSession()
	if err != nil {
		t.Skipf("no AWS session: %v", err)
	}
	if _, err := sess.Config.Credentials.Get(); err != nil {
		t.Skipf("no AWS credentials: %v", err)
	}
	raw := os.Getenv("UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY")
	if raw == "" {
		t.Skip("UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY not set")
	}
	pk, err := solana.PrivateKeyFromSolanaKeygenFileBytes([]byte(raw))
	if err != nil {
		t.Fatalf("cannot parse authority key: %v", err)
	}
	if !pk.PublicKey().Equals(airdrop.AuthorityPub) {
		t.Fatalf("authority key pubkey %s does not match expected %s", pk.PublicKey(), airdrop.AuthorityPub)
	}
	return pk
}

func e2eTokenBalance(t *testing.T, client *rpc.Client, owner solana.PublicKey) uint64 {
	t.Helper()
	ata, _, err := solana.FindAssociatedTokenAddress(owner, airdrop.ExpectedMint)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.GetTokenAccountBalance(context.Background(), ata, rpc.CommitmentConfirmed)
	if err != nil {
		return 0 // ATA absent counts as zero
	}
	var v uint64
	fmt.Sscan(res.Value.Amount, &v)
	return v
}

func TestE2EActivationDistributesUUSDC(t *testing.T) {
	authority := requireE2E(t)
	recipient := e2eRecipient()
	rpcClient := rpc.New(e2eRPCURL())

	// Fresh run: remove any previous job for the recipient in the TEST table.
	svc := dynamodb.New(awssession.Must(awssession.NewSession()))
	if _, err := svc.DeleteItem(&dynamodb.DeleteItemInput{
		TableName: aws.String(e2eTableName),
		Key:       map[string]*dynamodb.AttributeValue{"address": {S: aws.String(recipient)}},
	}); err != nil {
		t.Fatalf("clearing previous job: %v", err)
	}

	store, err := airdrop.NewDynamoStore(e2eTableName)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := airdrop.NewChain(e2eRPCURL(), authority)
	if err != nil {
		t.Fatal(err)
	}
	dist := airdrop.New(store, chain)

	k, _ := cipher.GenerateKey(32)
	app := &App{
		db:       data.NewMockDBContent([]string{sponsor}),
		jwt:      crypto.NewJWTHS256(k),
		mailer:   &mailer.MockSmtpMailer,
		wg:       sync.WaitGroup{},
		rl:       limiter.NewUnlimited(),
		secpath1: "path1",
		secpath2: "path2",
		c:        cache.New(),
		apiKey:   testApiKey,
		dist:     dist,
	}
	r := setupRouter(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dist.Run(ctx)

	before := e2eTokenBalance(t, rpcClient, solana.MustPublicKeyFromBase58(recipient))

	// Activate through the real HTTP handler.
	vt, err := app.jwt.Create(&data.User{Address: recipient, Email: "e2e@unleak.trade", Sponsor: sponsor}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", fmt.Sprintf("/activate/%s/%s", vt, app.jwt.Hash(vt)), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("activation: got %d, want 201: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Airdrop *airdrop.Response `json:"airdrop"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Airdrop == nil {
		t.Fatal("201 response has no airdrop object")
	}
	if resp.Airdrop.Status != airdrop.StatusPending && resp.Airdrop.Status != airdrop.StatusProcessing {
		t.Fatalf("initial airdrop status = %s, want pending/processing", resp.Airdrop.Status)
	}
	if resp.Airdrop.Symbol != airdrop.Symbol {
		t.Errorf("airdrop symbol = %q, want %q", resp.Airdrop.Symbol, airdrop.Symbol)
	}

	// Wait for the worker to confirm the payout on devnet.
	deadline := time.Now().Add(120 * time.Second)
	var j *airdrop.Job
	for {
		j, err = store.Get(recipient)
		if err == nil && j.Status == airdrop.StatusConfirmed {
			break
		}
		if err == nil && j.Status == airdrop.StatusFailedTerminal {
			t.Fatalf("distribution failed terminally: %s %s", j.LastErrorCode, j.LastErrorMsg)
		}
		if time.Now().After(deadline) {
			status := "<missing>"
			if j != nil {
				status = fmt.Sprintf("%s (%s %s)", j.Status, j.LastErrorCode, j.LastErrorMsg)
			}
			t.Fatalf("distribution not confirmed within deadline; job: %s", status)
		}
		time.Sleep(3 * time.Second)
	}
	if j.Signature == "" {
		t.Fatal("confirmed job has no transaction signature")
	}
	t.Logf("✅ confirmed: https://explorer.solana.com/tx/%s?cluster=devnet", j.Signature)

	after := e2eTokenBalance(t, rpcClient, solana.MustPublicKeyFromBase58(recipient))
	if after-before != airdrop.RawAmount {
		t.Fatalf("on-chain balance delta = %d, want exactly %d", after-before, airdrop.RawAmount)
	}
}
