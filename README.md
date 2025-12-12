# Go-LLM-Router

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)](https://golang.org)
![Docker Image Size (tag)](https://img.shields.io/docker/image-size/zqverse0/llm-gateway/latest)

[English](#english) | [简体中文](#chinese)

---

<div id="english"></div>

## 📖 English

> **High-performance, stateless LLM gateway with intelligent load balancing and failover.**

Go-LLM-Router is an enterprise-grade, production-ready API gateway designed for Large Language Models. Built with Go and Gin, it offers seamless load balancing, intelligent circuit breaking, and Docker optimization (~40MB).

### 🚀 Key Features

* **🔄 Multi-Strategy Routing**:
    * **Round-Robin**: Distributes traffic across multiple API keys to balance token usage.
    * **Fallback**: Automatically tries the next key/model upon 401/429 errors.
    * **Pinned Mode**: Route to a specific channel using `model$index` syntax (e.g., `Ai-chat$2`).
* **🛡️ Smart Circuit Breaker**:
    * **Soft Errors**: Retries on Auth/RateLimit errors.
    * **Hard Errors**: Skips models immediately on 404/Connection Refused to prevent latency.
    * **Empty Key Skip**: Automatically bypasses models with no configured keys.
* **⚡ Lightweight**: Zero dependencies (Embedded SQLite), starts instantly.
* **🔌 OpenAI Compatible**: Full support for Streaming, Non-Streaming, and Vision (Multimodal) requests.

### 🛠️ Quick Start
<img width="1493" height="998" alt="image" src="https://github.com/user-attachments/assets/38a69051-3685-442e-a168-c9aa314886eb" />

#### Option 1: Docker Run (Recommended)

```bash
docker run -d \
  --name go-llm-router \
  -p 8000:8000 \
  -v $(pwd)/data:/app/data \
  zqverse0/llm-gateway:latest
Option 2: Docker Compose
Create a docker-compose.yml:

YAML

version: '3.8'
services:
  go-llm-router:
    image: zqverse0/llm-gateway:latest
    container_name: go-llm-router
    ports:
      - "8000:8000"
    volumes:
      - ./data:/app/data
    restart: unless-stopped
Then run:

Bash

docker-compose up -d
Dashboard Access
Open your browser and navigate to http://localhost:8000/demo to access the web management interface.

<div id="chinese"></div>

📖 简体中文
高性能、无状态的 LLM 企业级网关，专注于负载均衡与故障转移。

Go-LLM-Router 是一个基于 Go (Gin) 开发的轻量级大模型网关。它不依赖 Redis 或 MySQL，仅需一个 Docker 镜像即可提供企业级的高可用接入能力。

🚀 核心功能
🔄 多策略路由 (Routing):

负载均衡 (Round-Robin): 支持多 Key 轮询，自动均摊 Token 消耗，避免单 Key 限速。

故障转移 (Failover): 遇到 401/429 错误自动重试下一个 Key；遇到 502 自动切换备用模型。

定向路由 (Pinned Mode): 支持通过 模型名$序号 (如 Ai-chat$2) 强制指定使用第几个 Key，便于测试或计费隔离。

🛡️ 智能熔断 (Circuit Breaker):

软错误: 认证失败、限流时自动重试。

硬错误: 遇到 404 或网络拒接时，立即跳过当前模型，防止无效等待。

空 Key 跳过: 自动检测并跳过未配置 Key 的模型组。

⚡ 极简架构: 零外部依赖 (内置 SQLite)，Docker 镜像仅 ~40MB，启动即用。

🔌 完美兼容: 100% 兼容 OpenAI 接口格式，支持流式 (Stream) 和多模态 (Vision) 请求。

🛠️ 快速开始
方式一：Docker 启动 (推荐)
Bash

docker run -d \
  --name go-llm-router \
  -p 8000:8000 \
  -v $(pwd)/data:/app/data \
  zqverse0/llm-gateway:latest
方式二：Docker Compose
创建 docker-compose.yml:

YAML

version: '3.8'
services:
  go-llm-router:
    image: zqverse0/llm-gateway:latest # 请替换为你实际的镜像名
    container_name: go-llm-router
    ports:
      - "8000:8000"
    volumes:
      - ./data:/app/data
    restart: unless-stopped
启动服务：

Bash

docker-compose up -d
⚙️ 配置指南
本项目采用 可视化配置，无需手写配置文件。

浏览器访问 http://localhost:8000/demo 进入管理后台。

创建模型组: 例如 Group ID 填 gpt-4，策略选 round_robin。

添加模型: 填写上游渠道（如 OpenAI, DeepSeek, Azure）。

添加密钥: 为每个模型配置多个 Key。

热重载: 点击保存，配置立即生效，无需重启容器。

💻 调用示例
Python (OpenAI SDK)
Python

import openai

client = openai.OpenAI(
    api_key="sk-any-key",  # 网关内部管理真实 Key，此处随便填
    base_url="http://localhost:8000/v1"
)

# 1. 普通负载均衡请求
response = client.chat.completions.create(
    model="gpt-4", # 对应后台配置的 Group ID
    messages=[{"role": "user", "content": "你好"}]
)

# 2. 定向路由请求 (强制使用第 2 个 Key)
response = client.chat.completions.create(
    model="gpt-4$1", # 索引从 0 开始，$1 代表第 2 个
    messages=[{"role": "user", "content": "你好"}]
)
💻 本地开发
Bash

# 克隆项目
git clone https://github.com/zqverse0/Go-LLM-Router.git
cd Go-LLM-Router

# 安装依赖
go mod download

# 运行
go run ./cmd
🤝 贡献 (Contributing)
欢迎提交 Pull Request 或 Issue！

📄 协议 (License)
本项目基于 MIT License 开源。
