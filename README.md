# Go-LLM-Router

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)](https://golang.org)

> **High-performance, stateless LLM gateway with intelligent load balancing and failover.**

Go-LLM-Router is an enterprise-grade, production-ready API gateway for Large Language Models. Built with Go and Gin framework, it provides seamless load balancing, intelligent failover, and circuit breaker capabilities to ensure high availability and optimal performance for your LLM applications.

![Dashboard Screenshot](screenshot.png)
*(Optional: Place a screenshot.png in your repo to show off the dashboard)*

## Ì†ΩÌ∫Ä Features

### Ì†ΩÌ¥Ñ Multi-Strategy Routing
- **Round-Robin**: Automatic load distribution across multiple API keys with token consumption balancing.
- **Fallback**: Intelligent error-based failover. If one key fails (401/429), it automatically tries the next.
- **Pinned Mode**: Direct routing using `model$index` syntax (e.g., `Ai-chat$2`) for specific channel selection/testing.

### Ì†ΩÌª°Ô∏è Smart Circuit Breaker
- **Soft Error Handling**: Automatic retry on 401/429/5xx errors.
- **Hard Error Detection**: Immediate model switching on 404/Connection Refused errors to prevent wasted retries.
- **Empty Key Skip**: Automatically skips models with no valid keys configured.

### ‚ö° Lightweight Architecture
- **Zero External Dependencies**: Embedded SQLite database. No Redis/MySQL required.
- **Docker Optimized**: Ultra-lightweight container image (~40MB) with multi-stage builds.
- **Built-in Dashboard**: Web management interface at `/demo` with hot-reload configuration.

### Ì†ΩÌ¥å OpenAI Compatible
- Full compatibility with OpenAI API format.
- Support for both **Streaming** and **Non-Streaming** responses.
- Automatic parsing of **Multimodal (Vision)** requests.

## Ì†ΩÌª†Ô∏è Quick Start

### Option 1: Docker Run (Recommended)

```bash
# Pull and run (Assuming you build it locally as llm-gateway:latest)
docker run -d \
  --name go-llm-router \
  -p 8000:8000 \
  -v $(pwd)/data:/app/data \
  llm-gateway:latest
Option 2: Docker Compose
Create a docker-compose.yml:

YAML

version: '3.8'
services:
  go-llm-router:
    build: .  # Build from source
    container_name: go-llm-router
    ports:
      - "8000:8000"
    volumes:
      - ./data:/app/data
    environment:
      - GIN_MODE=release
    restart: unless-stopped
Then run:

Bash

docker-compose up -d
Access Dashboard
Open your browser and navigate to http://localhost:8000/demo to access the web management interface.

‚öôÔ∏è Configuration
No config files needed! This project uses a built-in Dashboard for management.

Navigate to http://localhost:8000/demo.

Create a Model Group: e.g., Group ID gpt-4, Strategy round_robin.

Add Models: Add upstream providers (e.g., OpenAI, Azure, DeepSeek).

Add Keys: Add multiple API keys for each model.

Changes are applied immediately (Hot-Reload).

Ì†ΩÌ≥ñ Usage Guide
Standard OpenAI SDK Usage
The gateway is fully compatible with the official OpenAI SDK. Just change the base_url.

Python

import openai

client = openai.OpenAI(
    api_key="sk-any-key",  # The gateway handles real keys internally
    base_url="http://localhost:8000/v1"
)

# Standard chat completion
response = client.chat.completions.create(
    model="gpt-3.5-turbo", # Matches your Group ID in Dashboard
    messages=[{"role": "user", "content": "Hello!"}]
)
Advanced: Pinned Mode Routing
Force usage of a specific model/key combination for testing or billing separation:

Python

# Use the 2nd model/key in the "Ai-chat" group
response = client.chat.completions.create(
    model="Ai-chat$2", 
    messages=[{"role": "user", "content": "Hello!"}]
)
Ì†ΩÌ≤ª Development
Prerequisites
Go 1.21+

Docker (optional)

Local Build
Bash

# Clone the repository
git clone [https://github.com/zqverse0/Go-LLM-Router.git](https://github.com/zqverse0/Go-LLM-Router.git)
cd Go-LLM-Router

# Install dependencies
go mod download

# Run
go run ./cmd
Ì†æÌ¥ù Contributing
We welcome contributions! Please feel free to submit a Pull Request.

Ì†ΩÌ≥Ñ License
This project is licensed under the MIT License - see the LICENSE file for details.
