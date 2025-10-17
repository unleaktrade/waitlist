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

## Technology Stack

- **Language**: Go (94.3%)
- **Deployment**: Heroku
- **License**: MIT

## Contributing

This repository is a fork of fairhive-labs/preregister, adapted for UnleakTrade's specific requirements.
