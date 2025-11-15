# Syrinx

A distributed, P2P content-distribution platform

## Values

Syrinx is built on the following values:

* It's your attention: No push notifications, no alerts, no interruptions.
* It's your platform: Syrinx will never push content to your feed you didn't subscribe to.
* It's your time: No infinite scroll, no engagement optimization, no dark patterns.
* It's your decision: We give you control over your data, even at the expense of convenience.
* It's your privacy: Private messages are never routed through the server, they are delivered directly to the recipient, encrypted.
* It's your right: No tracking, no analytics, no data collection.
* It's our promise: Syrinx is free and open source, and will remain so.

## Security

### Password Recovery Design

For security reasons, your password serves dual purposes: it authenticates you against the API and encrypts your private key. This design is crucial for protecting your data in scenarios where private keys are compromised (for example, through a software vulnerability that grants an attacker access to your key). Even with access to your private key, the attacker would still need your password to decrypt your messages.

However, this security feature comes with a trade-off: if you lose your password, all encrypted messages stored on your device become permanently inaccessible. This makes traditional "forgot password" functionality impossible, as there is no way to recover access to your encrypted data without the original password.

This is why Syrinx enforces strict password strength requirements. While API authentication includes artificial delays to prevent rainbow table attacks, these protections don't exist if an attacker gains access to your private key. Without these safeguards, an attacker could test thousands of passwords per second. A 16-character password containing uppercase letters, lowercase letters, numbers, and symbols would take years to brute force, providing strong protection for your encrypted data.

## Architecture

Syrinx consists of two main components:

- **HTTP API Server**: Handles REST API requests, user authentication, and content management
- **Realtime WebSocket Service**: Handles real-time WebSocket connections and message broadcasting

The services communicate via Go channels for low-latency, type-safe message passing.

## Setup

### Prerequisites

- Docker and Docker Compose (recommended)
- OR Go 1.21+ and PostgreSQL (manual setup)

### Observability

This project uses **SigNoz** for observability with full RBAC support.

### Quick Start with Docker (Recommended)

1. **Clone and navigate to the project:**
   ```bash
   git clone https://github.com/var0xyz/syrinx.git
   cd ./syrinx
   ```

2. **Start all services with Docker Compose:**
   ```bash
   make run
   # or
   docker-compose up --build
   ```

3. **Access the application:**
  ```bash
  $ curl http://localhost:9000/
  404 page not found
  ```

4. **Stop the application:**
   ```bash
   make stop
   # or
   docker-compose down
   ```

### Manual Setup (Alternative)

1. **Install dependencies:**
   ```bash
   make install
   # or manually:
   # go mod download
   ```

2. **Set up PostgreSQL database:**
   ```bash
   createdb syrinx
   ```

3. **Configure environment variables:**
   ```bash
   cp .env.example .env
   ```

4. **Run the services:**
   ```bash
   go run main.go
   ```

## Why would I use a system that can't delete my data if I want to?

This is how the internet actually works. Centralized services give us the
illusion of control with their endless privacy policies and corporate speak,
but they have no real control over the data. Once a message is displayed on
someone's screen, that message has been downloaded to the user's computer and
they control it. They can copy it, screenshot it, save it... *they* are in
control, not the server.

Syrinx embraces this reality, puts it in your face, and tells you how things
really work. Syrinx doesn't lie to you about data permanence. Instead, it
empowers you to take calculated risks by not using your real identity while
still being able to verify the authenticity of your posts through
cryptographic signatures.
