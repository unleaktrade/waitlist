# UnleakTrade - Waitlist Service

A lightweight microservice that powers the waitlist registration system for [UnleakTrade](https://unleak.trade), a privacy-focused OTC/RFQ trading platform built on Solana.

## Overview

This microservice handles pre-registration during the waitlist phase of UnleakTrade, collecting and securely storing user information from early adopters interested in accessing the platform.

### Features

- Simple REST API for waitlist registration
- Email and blockchain address validation
- X/Twitter account (username)
- Secure hash generation for each registration
- Minimal dependencies and fast deployment

## About UnleakTrade

UnleakTrade is a decentralized trading platform that enables private, secure OTC (Over-The-Counter) and RFQ (Request for Quote) transactions on the Solana blockchain. The platform leverages zero-knowledge proofs and blockchain technology to provide institutional-grade privacy for cryptocurrency trading.

Learn more at [unleak.trade](https://unleak.trade).

## Documentation

Complete workflow details and sequence diagrams will be available very soon.

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
| `UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY` | solana-keygen JSON array of the authority keypair. Missing/invalid → service boots with distribution **disabled** (loud `🚨` log), activation unaffected. |
| `UNLEAKTRADE_SOLANA_RPC_URL` | defaults to `https://api.devnet.solana.com` |
| `UNLEAKTRADE_AIRDROP_TABLE_NAME` | defaults to `Airdrop` (DynamoDB, partition key `address` (S)) |
| `UNLEAKTRADE_AIRDROP_ENABLED` | defaults to `true`; `false` disables enqueueing, the worker, and the `airdrop` response key |

The service persists jobs in DynamoDB: table `Airdrop` in production and `Airdrop_TEST` for the live test suite (same schema, partition key `address` (S), on-demand billing).

Deploys are currently manual (`workflow_dispatch`), so config vars are set directly on the Heroku app:

```sh
heroku config:set UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY="$(cat maker.json)" -a unleaktrade-waitlist
heroku config:set UNLEAKTRADE_AIRDROP_TABLE_NAME="Airdrop" -a unleaktrade-waitlist            # optional, default
heroku config:set UNLEAKTRADE_SOLANA_RPC_URL="https://api.devnet.solana.com" -a unleaktrade-waitlist  # optional, default
```

For workflow-driven deploys, the equivalent GitHub repo secret is `HD_UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY` (the deploy action strips the `HD_` prefix into a config var):

```sh
gh secret set HD_UNLEAKTRADE_DISTRIBUTION_AUTHORITY_KEY --repo unleaktrade/waitlist < maker.json
```

### Operator runbook

- **Balances**: keep the authority above **0.05 SOL** and **20 payouts** of tokens — the worker logs `🚨` alerts below those thresholds and marks jobs `failed_terminal`/`insufficient_distributor_inventory` when a payout is impossible (< 0.005 SOL or < 1000 tokens). Top up with devnet SOL (faucet) and an SPL transfer of the token from treasury — minting is impossible.
- **Reconciliation**: automatic at boot and every 30s — `processing` jobs with expired leases re-enter the queue. Inspect state: `GET /<p1>/<p2>/airdrop/list` (header `UNLK-API-KEY`).
- **Manual retry**: `POST /<p1>/<p2>/airdrop/<address>/retry` re-arms a `failed_*` job (or enqueues from scratch for an activated address whose job is missing). Confirmed jobs can never be re-armed.
- **Explorer**: every confirmed job stores its transaction signature — `https://explorer.solana.com/tx/<signature>?cluster=devnet`.
- **Mint drift**: worker halts and logs `🚨 MINT CONFIG DRIFT`; fix the campaign constants in `internal/airdrop/airdrop.go`, redeploy, then retry affected jobs.

## Technology Stack

- **Language**: Go (94.3%)
- **Deployment**: Heroku
- **License**: MIT

## Contributing

This repository is a fork of fairhive-labs/preregister, adapted for UnleakTrade's specific requirements.
