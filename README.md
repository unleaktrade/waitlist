# UnleakTrade - Waitlist Service

A lightweight microservice that powers the waitlist registration system for [UnleakTrade](https://unleak.trade), a privacy-focused OTC/RFQ trading platform built on Solana.

## Overview

This microservice handles pre-registration during the waitlist phase of UnleakTrade, collecting and securely storing user information from early adopters interested in accessing the platform.

### Features

- Simple REST API for waitlist registration
- Email and blockchain address validation
- Sponsor (referral) address: registration requires the Solana address of an already activated member
- Secure hash generation for each registration
- Minimal dependencies and fast deployment

## About UnleakTrade

UnleakTrade is a decentralized trading platform that enables private, secure OTC (Over-The-Counter) and RFQ (Request for Quote) transactions on the Solana blockchain. The platform uses a commit-reveal protocol and signed liquidity attestations to keep quotes confidential until reveal (zero-knowledge proofs are on the roadmap).

Learn more at [unleak.trade](https://unleak.trade).

## Documentation

Interactive API documentation is served by the service itself: Swagger UI at `/doc` (also the target of `/`), OpenAPI spec at `/openapi.json`.

### Flow

1. `POST /register` with the wallet address, email and sponsor address. The service answers `202` with a `hash` and emails an activation link, `https://unleak.trade/activate/<token>`.
2. The landing page calls `POST /activate/<token>/<hash>`. The token is a JWT valid for **10 minutes**. A successful activation answers `201` and, when the airdrop is enabled, embeds its status (see below).

### Endpoints

| Method and path | Access | Purpose |
| --- | --- | --- |
| `POST /register` | public | Start a registration |
| `POST /activate/:token/:hash` | public | Activate a registration |
| `GET /health` | public | Health check (`{"status":"ok"}`), used by the deploy |
| `GET /doc`, `GET /swagger/*`, `GET /openapi.json` | public | API documentation |
| `GET /check-wallet/:address` | `UNLK-API-KEY` header | Check whether a wallet is on the waitlist |
| `GET /<p1>/<p2>/list` | `UNLK-API-KEY` header | List registrations (`offset`, `max`, `mime=csv`) |
| `GET /<p1>/<p2>/airdrop/list` | `UNLK-API-KEY` header | List airdrop jobs |
| `POST /<p1>/<p2>/airdrop/<address>/retry` | `UNLK-API-KEY` header | Re-arm a failed airdrop job |

`<p1>/<p2>` are the two secret path segments from `UNLEAKTRADE_API_SECURE_PATH1` / `UNLEAKTRADE_API_SECURE_PATH2`. All routes are rate limited per client IP (0.1 request per second sustained, burst of 10) and served with permissive CORS.

### Service configuration

| Env var | Required | Purpose |
| --- | --- | --- |
| `UNLEAKTRADE_ENCRYPTION_KEY` | **yes** | Encryption key; the service panics at boot without it |
| `UNLEAKTRADE_API_SECURE_PATH1`, `UNLEAKTRADE_API_SECURE_PATH2` | **yes** | Secret path segments of the operator routes |
| `UNLEAKTRADE_WAITLIST_API_KEY` | **yes** | Expected value of the `UNLK-API-KEY` header |
| `UNLEAKTRADE_WAITLIST_TABLE_NAME` | no | DynamoDB table, defaults to `Waitlist` |
| `UNLEAKTRADE_MAIL_USER`, `UNLEAKTRADE_MAIL_PASSWORD` | no | Mailtrap SMTP credentials for the activation email |
| `PORT` | no | Listen port, defaults to `8080` |

AWS credentials and region come from the default AWS SDK chain. The airdrop variables are listed in the next section.

### Local development

```sh
make run     # build and start ./bin/api
make build
make test    # go test ./... (live DynamoDB / Mailtrap tests skip without credentials)
```

## Unleak USDC (uUSDC) Airdrop

**Unleak USDC (`uUSDC`)** — mint `5jBqJmY2mKetudVa2XaC8U6UN2BNNirDiTnDEuA6pdyR`, 6 decimals, classic SPL token program — is UnleakTrade's test version of USDC on Solana devnet: it mimics USDC in every beta trading flow, but it is **not real USDC**, has **no real-world value**, and cannot be bought, sold, or bridged.

After a successful waitlist activation, the service asynchronously distributes **1000 uUSDC** to the activated wallet. See [issue #18](https://github.com/unleaktrade/waitlist/issues/18) for the full design.

Key properties:

- **Transfer-only**: the mint's supply is already `u64::MAX`, so minting is impossible. Payouts are `TransferChecked` from the pre-funded distribution authority `7QVKMkpbpniPhDu3V3iXTauDwkUfaAiSZ9QrqfQioEDy`, which also pays ATA creation rent.
- **Idempotent**: one DynamoDB job per wallet (conditional writes); duplicate activations, worker retries, and concurrent workers can never produce a second payout.
- **Non-blocking**: activation always succeeds even when distribution is delayed or fails. The activation `201` response embeds an `airdrop` object (`pending | processing | confirmed | already_distributed | failed_retryable | failed_terminal`, with `symbol: "uUSDC"`); there is deliberately no polling endpoint.
- **Mint drift guard**: the worker verifies the configured mint against the beta app's on-chain `Config.usdcMint` (PDA `GgE8H46in1MLh8EJs6Je7ps7wEysiEks7ZcMVVgq4xTZ`) at startup and every 10 minutes. On drift, distribution halts (jobs queue as `pending`) until an operator updates the campaign constants and redeploys.

### Configuration

| Env var | Behavior |
| --- | --- |
| `UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY` | solana-keygen JSON array of the authority keypair. Missing, unparsable, or not matching the expected authority pubkey → service boots with distribution **disabled** (loud `🚨` log), activation unaffected. |
| `UNLEAKTRADE_SOLANA_RPC_URL` | defaults to `https://api.devnet.solana.com` |
| `UNLEAKTRADE_AIRDROP_TABLE_NAME` | defaults to `Airdrop` (DynamoDB, partition key `address` (S)) |
| `UNLEAKTRADE_AIRDROP_ENABLED` | defaults to `true`; `false` disables enqueueing, the worker, and the `airdrop` response key |

The service persists jobs in DynamoDB: table `Airdrop` in production and `Airdrop_TEST` for the live test suite (same schema, partition key `address` (S), on-demand billing). The registration tests use `Waitlist_TEST` the same way.

Deploys are currently manual (`workflow_dispatch`), so config vars are set directly on the Heroku app:

```sh
heroku config:set UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY="$(cat maker.json)" -a unleaktrade-waitlist
heroku config:set UNLEAKTRADE_AIRDROP_TABLE_NAME="Airdrop" -a unleaktrade-waitlist            # optional, default
heroku config:set UNLEAKTRADE_SOLANA_RPC_URL="https://api.devnet.solana.com" -a unleaktrade-waitlist  # optional, default
```

The GitHub workflow deliberately does **not** manage config vars (empty secrets would overwrite live values with blanks); it only tests (`go vet`, `go test -race` with coverage, on every push and pull request to `main`) and, on manual dispatch from `main`, pushes the code to Heroku. That deploy job needs one repo secret:

```sh
gh secret set HEROKU_API_KEY --repo unleaktrade/waitlist --body "$(heroku auth:token)"
```

Live tests (DynamoDB `*_TEST` tables, Mailtrap sandbox) skip when credentials are absent; add `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_REGION` (and `MAILTRAP_USER` / `MAILTRAP_PASSWORD`) as repo secrets to re-enable them in CI. The devnet distribution e2e additionally requires `UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY` in the local environment and is intended to run locally, not in CI.

### Operator runbook

- **Balances**: keep the authority above **0.05 SOL** and **20 payouts** of tokens — the worker logs `🚨` alerts below those thresholds and marks jobs `failed_terminal`/`insufficient_distributor_inventory` when a payout is impossible (< 0.005 SOL or < 1000 tokens). Top up with devnet SOL (faucet) and an SPL transfer of the token from treasury — minting is impossible.
- **Reconciliation**: automatic at boot and every 30s — `processing` jobs with expired leases re-enter the queue. Inspect state: `GET /<p1>/<p2>/airdrop/list` (header `UNLK-API-KEY`).
- **Manual retry**: `POST /<p1>/<p2>/airdrop/<address>/retry` re-arms a `failed_*` job (or enqueues from scratch for an activated address whose job is missing). Confirmed jobs can never be re-armed.
- **Explorer**: every confirmed job stores its transaction signature — `https://explorer.solana.com/tx/<signature>?cluster=devnet`.
- **Mint drift**: worker halts and logs `🚨 MINT CONFIG DRIFT`; fix the campaign constants in `internal/airdrop/airdrop.go`, redeploy, then retry affected jobs.

## Technology Stack

- **Language**: Go 1.25 (Gin, AWS SDK for Go with DynamoDB, `gagliardetto/solana-go`)
- **Deployment**: Heroku (`Procfile`: `web: ./bin/api`)
- **License**: MIT

## Contributing

This repository is a fork of fairhive-labs/preregister, adapted for UnleakTrade's specific requirements.
