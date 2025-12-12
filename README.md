# Go-LLM-Router

[![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[English](#english) | [简体中文](#chinese)

---

<a id="english"></a>
## 📖 English

> **A lightweight, stateless LLM gateway built for learning and self-hosting.**

**Go-LLM-Router** is an experimental project exploring high-availability architectures for Large Language Models. Built with Go and Gin, it aims to solve the problem of API key exhaustion and provider instability through intelligent routing strategies.

⚠️ **Note**: This project is currently in active development. It is designed for personal study and self-hosting, not yet for mission-critical enterprise environments. Contributions and bug reports are highly welcome!

### 🚀 Key Features

* **🔄 Routing Strategies**:
    * **Round-Robin**: Basic load balancing across multiple API keys.
    * **Failover**: Automatically retries the next key on 401/429 errors.
    * **Pinned Mode**: Direct access to a specific key using `model$index` syntax (e.g., `Ai-chat$2`).
* **🛡️ Circuit Breaker**: Skips models on Hard Errors (404/Connection Refused) to prevent latency spikes.
* **⚡ Simple Architecture**: No Redis/MySQL required. Uses embedded SQLite.
* **🔌 Compatibility**: Supports standard OpenAI API format (Stream & Non-Stream).

### 🛠️ Getting Started

#### Option 1: Run from Source (For Developers)

Prerequisites: **Go 1.21+**, **GCC** (for SQLite CGO).

```bash
# 1. Clone the repo
git clone [https://github.com/zqverse0/Go-LLM-Router.git](https://github.com/zqverse0/Go-LLM-Router.git)
cd Go-LLM-Router

# 2. Install dependencies
go mod download

# 3. Run
go run ./cmd
````

The server will start at `http://localhost:8000`.

#### Option 2: Docker (For Deployment)

If you just want to use it without setting up Go environment:

```bash
docker run -d \
  --name go-llm-router \
  -p 8000:8000 \
  -v $(pwd)/data:/app/data \
  zqverse0/llm-gateway:latest
```

### ⚙️ Configuration

Visit `http://localhost:8000/demo` to configure your Model Groups and API Keys via the web dashboard. Changes are applied immediately (Hot-Reload).

### 🤝 Contributing

This is an open-source learning project. I welcome any suggestions, PRs, or issues to help improve the code quality and logic.

-----

<a id="chinese"></a>

## 📖 简体中文

> **一个轻量级、无状态的 LLM 网关，专注于负载均衡与高可用架构的学习与实践。**

**Go-LLM-Router** 是一个基于 Go (Gin) 开发的大模型网关项目。开发的初衷是为了解决个人或小团队在使用 LLM 时遇到的 Key 限速、接口不稳定等问题，同时探索高并发下的路由策略实现。

⚠️ **说明**: 本项目目前处于早期开发阶段，旨在共同学习和交流，建议用于个人项目或测试环境。如果您发现了 Bug 或有更好的实现思路，非常欢迎提交 Issue 或 PR！

### 🚀 核心功能

  * **🔄 多策略路由**:
      * **负载均衡 (Round-Robin)**: 多 Key 轮询，均摊 Token 消耗。
      * **故障转移 (Failover)**: 遇到 401/429 等错误自动重试下一个 Key。
      * **定向路由 (Pinned Mode)**: 支持通过 `模型名$序号` (如 `Ai-chat$2`) 强制指定使用第几个 Key，方便调试。
  * **🛡️ 熔断机制**: 遇到 404 或网络拒接等硬错误时，自动跳过当前模型，防止无效等待。
  * **⚡ 极简架构**: 零外部依赖 (内置 SQLite)，无 Redis/MySQL 负担。
  * **🔌 完美兼容**: 兼容 OpenAI 接口格式，支持流式 (Stream) 和多模态 (Vision) 请求。

### 🛠️ 快速开始

#### 方式一：源码运行 (推荐开发者)

环境要求: **Go 1.21+**, **GCC** (因为使用了 SQLite，需要 CGO 支持)。

```bash
# 1. 克隆项目
git clone [https://github.com/zqverse0/Go-LLM-Router.git](https://github.com/zqverse0/Go-LLM-Router.git)
cd Go-LLM-Router

# 2. 安装依赖
go mod download

# 3. 运行
go run ./cmd
```

服务默认运行在 `8000` 端口。

#### 方式二：Docker 运行 (推荐部署)

如果你不想配置 Go 环境，可以直接使用 Docker：

```bash
docker run -d \
  --name go-llm-router \
  -p 8000:8000 \
  -v $(pwd)/data:/app/data \
  zqverse0/llm-gateway:latest
```

### ⚙️ 配置指南

本项目内置了可视化管理界面，无需手写配置文件。
启动后访问 `http://localhost:8000/demo` 即可添加模型组和 Key。配置保存即生效（热重载）。

### 🤝 参与贡献

这是一个开源学习项目，代码中可能存在不足之处。
如果你对 Go 语言、高并发架构感兴趣，欢迎 fork 本项目并提交修改。让我们一起完善它！

## 📄 协议 (License)

[MIT License](https://www.google.com/search?q=LICENSE)
