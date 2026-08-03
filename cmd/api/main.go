package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/unleaktrade/waitlist/internal/airdrop"
	"github.com/unleaktrade/waitlist/internal/cache"
	"github.com/unleaktrade/waitlist/internal/crypto"
	"github.com/unleaktrade/waitlist/internal/crypto/cipher"
	"github.com/unleaktrade/waitlist/internal/data"
	"github.com/unleaktrade/waitlist/internal/limiter"
	"github.com/unleaktrade/waitlist/internal/mailer"
)

type App struct {
	db                 data.DB
	jwt                crypto.Token
	mailer             mailer.Mailer
	wg                 sync.WaitGroup
	rl                 *limiter.RateLimiter
	secpath1, secpath2 string
	c                  *cache.Cache
	apiKey             string
	dist               *airdrop.Distributor // nil when distribution is disabled
}

var (
	jwts               = map[string]crypto.Token{}
	tableName          = "Waitlist"
	ek                 string
	secpath1, secpath2 string
	apiKey             string
	airdropTableName   = "Airdrop"
	airdropEnabled     = true
	authorityKey       solana.PrivateKey
	solanaRPCURL       = "https://api.devnet.solana.com"
)

func setup() {
	k, _ := cipher.GenerateKey(32)
	jwts["HS512"] = crypto.NewJWTHS512(k)
	k, _ = cipher.GenerateKey(16)
	jwts["HS256"] = crypto.NewJWTHS256(k)
	jwts["ES256"], _ = crypto.NewJWTES256()
	jwts["ES512"], _ = crypto.NewJWTES512()
	log.Println("🔐 JWT Services: OK")

	tn := os.Getenv("UNLEAKTRADE_WAITLIST_TABLE_NAME")
	if tn != "" {
		tableName = tn
	}
	log.Printf("💾 DynamoDB Table is %q\n", tableName)

	ek = os.Getenv("UNLEAKTRADE_ENCRYPTION_KEY")
	if ek == "" {
		panic("encryption key is missing")
	}
	log.Println("🔑 Encryption Key: OK")

	secpath1 = os.Getenv("UNLEAKTRADE_API_SECURE_PATH1")
	if secpath1 == "" {
		panic("secure path #1 must be set")
	}
	secpath2 = os.Getenv("UNLEAKTRADE_API_SECURE_PATH2")
	if secpath2 == "" {
		panic("secure path #1 must be set")
	}

	apiKey = os.Getenv("UNLEAKTRADE_WAITLIST_API_KEY")
	if apiKey == "" {
		panic("waitlist api-key must be set")
	}

	if tn := os.Getenv("UNLEAKTRADE_AIRDROP_TABLE_NAME"); tn != "" {
		airdropTableName = tn
	}
	if url := os.Getenv("UNLEAKTRADE_SOLANA_RPC_URL"); url != "" {
		solanaRPCURL = url
	}
	if os.Getenv("UNLEAKTRADE_AIRDROP_ENABLED") == "false" {
		airdropEnabled = false
		log.Println("🪂 Airdrop distribution: disabled by configuration")
		return
	}
	// The authority keypair is optional at boot: activation must keep working
	// even when distribution cannot. Never log the key material itself.
	authorityKey = nil
	raw := os.Getenv("UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY")
	if raw == "" {
		airdropEnabled = false
		log.Println("🚨 Airdrop distribution DISABLED: UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY is not set")
		return
	}
	pk, err := solana.PrivateKeyFromSolanaKeygenFileBytes([]byte(raw))
	if err != nil {
		airdropEnabled = false
		log.Printf("🚨 Airdrop distribution DISABLED: cannot parse authority key: %v", err)
		return
	}
	if !pk.PublicKey().Equals(airdrop.AuthorityPub) {
		airdropEnabled = false
		log.Printf("🚨 Airdrop distribution DISABLED: authority key pubkey %s does not match expected %s", pk.PublicKey(), airdrop.AuthorityPub)
		return
	}
	authorityKey = pk
	log.Printf("🪂 Airdrop distribution: enabled (authority %s, table %q)", pk.PublicKey(), airdropTableName)
}

func (app *App) initCache() {
	// fill cache
	users, err := app.db.List()
	if err != nil {
		panic("error loading users list from DB")
	}
	m := make(map[string]int64, len(users))
	for _, u := range users {
		m[u.Address] = u.Timestamp
	}

	c := cache.New()
	c.Fill(m)
	app.c = c
}

func newApp() *App {
	db, err := data.NewDynamoDB(tableName, ek)
	if err != nil {
		panic(err)
	}

	var dist *airdrop.Distributor
	if airdropEnabled && authorityKey != nil {
		store, err := airdrop.NewDynamoStore(airdropTableName)
		if err != nil {
			panic(err)
		}
		chain, err := airdrop.NewChain(solanaRPCURL, authorityKey)
		if err != nil {
			panic(err) // unreachable: pubkey already checked in setup()
		}
		dist = airdrop.New(store, chain)
	}

	return &App{
		db:       db,
		jwt:      jwts["ES256"],
		mailer:   mailer.New(os.Getenv("UNLEAKTRADE_MAIL_USER"), os.Getenv("UNLEAKTRADE_MAIL_PASSWORD"), "live.smtp.mailtrap.io", 587),
		wg:       sync.WaitGroup{},
		rl:       limiter.New(0.1, 10),
		secpath1: secpath1,
		secpath2: secpath2,
		apiKey:   apiKey,
		dist:     dist,
	}
}

func main() {
	setup()
	app := newApp()
	app.initCache()
	r := setupRouter(app)

	var addr string
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	} else {
		addr = ":8080" // default port
	}

	srv := &http.Server{
		Addr:           addr,
		Handler:        r,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   20 * time.Second,
		IdleTimeout:    time.Minute,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	if app.dist != nil {
		app.wg.Add(1)
		go func() {
			defer app.wg.Done()
			app.dist.Run(workerCtx)
		}()
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
		s := <-quit
		log.Printf("🚨 Shutdown signal \"%v\" received\n", s)

		log.Printf("🚦 Here we go for a graceful Shutdown...\n")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("⚠️ HTTP server Shutdown: %v", err)
		}
		cancelWorker() // stop the airdrop worker; unfinished jobs recover via lease expiry

		log.Printf("⏳ Waiting the end of all go-routines...")
		app.wg.Wait() // wait for all go-routines
		log.Printf("👍 go-routines are over")
		close(idleConnsClosed)
	}()

	go func() { // every 5 minutes, purge the rate limiters older than 10 minutes
		for {
			time.Sleep(5 * time.Minute)
			app.rl.Cleanup(10 * time.Minute)
		}
	}()

	log.Printf("✅ Listening and serving HTTP on %s\n", addr)
	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("👹 HTTP server ListenAndServe: %v", err)
	}

	<-idleConnsClosed
	log.Printf("😴 Server stopped")
}
