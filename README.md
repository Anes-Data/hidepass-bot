# HidePass Bot 🔐

HidePass Bot is a secure Telegram bot written in **Go** that enables users to embed AES-256 encrypted messages inside files and extract them safely using passphrase-based cryptography and steganography techniques.

---

## ✨ Features

* **Authenticated Encryption:** Uses **AES-256-GCM** to provide both confidentiality and authenticity.
* **Key Derivation:** Employs **Argon2id** for key derivation resistant to GPU/ASIC attacks.
* **Multi-Format Support:** Works with images (`.jpg`, `.png`), documents (`.pdf`, `.docx`), and plain text files (`.txt`) up to 10MB.
* **Privacy-First:** Processes files purely in-memory with automatic session timeouts.
* **Security Guardrails:** File signature validation (magic bytes), rate limiting, and command flood controls.

---

## 🛠️ Architecture & Cryptography

1. **Key Derivation:** Passphrase + Random Salt (16 bytes) ➔ **Argon2id** ➔ 256-bit Key.
2. **Encryption:** Plaintext + Key + Random Nonce (12 bytes) ➔ **AES-256-GCM** ➔ Ciphertext + Auth Tag.
3. **Payload Structure:** `[File Data] + [Salt] + [Nonce] + [Ciphertext] + [Length Header] + [Marker]`

---

## 🚀 Getting Started

### Prerequisites

* [Go 1.21+](https://go.dev/dl/)
* A Telegram Bot Token from [@BotFather](https://t.me/BotFather)

### Installation & Execution

```bash
# Clone the repository
git clone [https://github.com/Anes-Data/hidepass-bot.git](https://github.com/Anes-Data/hidepass-bot.git)
cd hidepass-bot

# Install dependencies
go mod download

# Set environment variable and run
export BOT_TOKEN="your_telegram_bot_token"
go run .