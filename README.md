# Syrinx

A distributed, P2P content-distribution platform

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
