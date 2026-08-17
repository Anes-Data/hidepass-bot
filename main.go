package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	MaxFileSize          = 10 * 1024 * 1024
	SessionIdleTimeout   = 1 * time.Hour
	fileDownloadTimeout  = 30 * time.Second
	
)

var httpClient = &http.Client{
	Timeout: fileDownloadTimeout,
}

var tokenRedactPattern = regexp.MustCompile(`bot\d+:[A-Za-z0-9_-]+`)

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return tokenRedactPattern.ReplaceAllString(err.Error(), "bot***REDACTED***")
}

type UserStep int

const (
	StepIdle UserStep = iota
	StepWaitingForFileToHide
	StepWaitingForTextToHide
	StepWaitingForPassphraseToHide
	StepWaitingForFileToExtract
	StepWaitingForPassphraseToExtract
)

type UserSession struct {
	Step          UserStep
	FileData      []byte
	FileName      string
	TextToHide    string
	EncryptedData []byte

	LastFileUploaded time.Time
	MenuClicks       int
	MenuWindowStart  time.Time
	LastActivity     time.Time

	procMu sync.Mutex
}

var (
	sessions = make(map[int64]*UserSession)
	mu       sync.Mutex

	allowedExtensions = map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".pdf":  true,
		".docx": true,
		".txt":  true,
	}

	magicSignatures = map[string][]byte{
		".jpg":  {0xFF, 0xD8, 0xFF},
		".jpeg": {0xFF, 0xD8, 0xFF},
		".png":  {0x89, 0x50, 0x4E, 0x47},
		".pdf":  {0x25, 0x50, 0x44},
		".docx": {0x50, 0x4B, 0x03, 0x04},
	}
)

func getSession(userID int64) *UserSession {
	mu.Lock()
	defer mu.Unlock()
	sess, exists := sessions[userID]
	if !exists {
		sess = &UserSession{
			Step:            StepIdle,
			MenuWindowStart: time.Now(),
		}
		sessions[userID] = sess
	}
	sess.LastActivity = time.Now()
	return sess
}

func startSessionCleanup() {
	ticker := time.NewTicker(15 * time.Minute)
	go func() {
		for range ticker.C {
			mu.Lock()
			now := time.Now()
			for id, s := range sessions {
				if now.Sub(s.LastActivity) > SessionIdleTimeout {
					s.FileData = nil
					s.EncryptedData = nil
					s.TextToHide = ""
					delete(sessions, id)
				}
			}
			mu.Unlock()
		}
	}()
}

func isFileSafe(fileName string, data []byte) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	if !allowedExtensions[ext] {
		return false
	}

	sig, hasSig := magicSignatures[ext]
	if !hasSig {
		return true
	}

	if len(data) < len(sig) {
		return false
	}
	for i, b := range sig {
		if data[i] != b {
			return false
		}
	}
	return true
}

func isMenuRateLimited(sess *UserSession) bool {
	now := time.Now()
	if now.Sub(sess.MenuWindowStart) > 5*time.Minute {
		sess.MenuWindowStart = now
		sess.MenuClicks = 1
		return false
	}

	sess.MenuClicks++
	return sess.MenuClicks > 3
}

func isFileRateLimited(sess *UserSession) bool {
	now := time.Now()
	return !sess.LastFileUploaded.IsZero() && now.Sub(sess.LastFileUploaded) < 5*time.Minute
}

func main() {
	// 1. خادم HTTP مصغر لإرضاء Port Check في Render
	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		})
		log.Printf("HTTP health check server listening on port %s", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// 2. تشغيل البوت
	botToken := os.Getenv("BOT_TOKEN")
	if botToken == "" {
		log.Fatal("BOT_TOKEN environment variable is required. Set it before running the bot.")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic("Failed to connect to Telegram: ", err)
	}

	bot.Debug = os.Getenv("BOT_DEBUG") == "true"
	log.Printf("Bot successfully started: %s", bot.Self.UserName)

	startSessionCleanup()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		go processUpdate(bot, update)
	}
}

func processUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered from panic while processing update: %v", r)
		}
	}()

	var chatID int64
	switch {
	case update.CallbackQuery != nil:
		chatID = update.CallbackQuery.Message.Chat.ID
	case update.Message != nil:
		chatID = update.Message.Chat.ID
	default:
		return
	}

	sess := getSession(chatID)

	sess.procMu.Lock()
	defer sess.procMu.Unlock()

	if update.CallbackQuery != nil {
		if isMenuRateLimited(sess) {
			bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "⏳ Please wait 5 minutes!"))
			msg := tgbotapi.NewMessage(chatID, "⏳ You have used the menu too many times. Please wait 5 minutes.")
			bot.Send(msg)
			return
		}

		handleCallback(bot, update.CallbackQuery, sess)
		return
	}

	if update.Message == nil {
		return
	}

	if update.Message.IsCommand() {
		if isMenuRateLimited(sess) {
			msg := tgbotapi.NewMessage(chatID, "⏳ Too many requests. Please wait 5 minutes before using commands again.")
			bot.Send(msg)
			return
		}

		switch update.Message.Command() {
		case "start", "reset":
			resetSensitiveData(sess)
			sess.Step = StepIdle
			sendMainMenu(bot, chatID)
		case "cancel":
			resetSensitiveData(sess)
			sess.Step = StepIdle
			msg := tgbotapi.NewMessage(chatID, "❌ Operation cancelled.")
			bot.Send(msg)
			sendMainMenu(bot, chatID)
		case "help":
			sendHelpMenu(bot, chatID)
		}
		return
	}

	switch sess.Step {
	case StepWaitingForFileToHide, StepWaitingForFileToExtract:
		if update.Message.Photo != nil {
			sendCancelMessage(bot, chatID, "⚠️ Please send the file as an **uncompressed Document** (File).")
			return
		}

		if update.Message.Document == nil {
			sendCancelMessage(bot, chatID, "⚠️ Please send a valid file as a Document:")
			return
		}

		if update.Message.Document.FileSize > MaxFileSize {
			sendCancelMessage(bot, chatID, "❌ File is too large! Maximum allowed size is 10 MB.")
			return
		}

		if isFileRateLimited(sess) {
			sendCancelMessage(bot, chatID, "⏳ Rate Limit Exceeded: You can only upload one file every 5 minutes.")
			return
		}

		fileData, fileName, err := downloadFile(bot, update.Message.Document.FileID, update.Message.Document.FileName)
		if err != nil {
			log.Printf("download error for chat %d: %s", chatID, sanitizeError(err))
			sendCancelMessage(bot, chatID, "❌ Failed to download the file. Please try again.")
			return
		}

		if !isFileSafe(fileName, fileData) {
			sendCancelMessage(bot, chatID, "❌ Invalid file type! Only images (JPG/PNG), PDF, TXT, and DOCX are allowed for your safety.")
			return
		}

		sess.LastFileUploaded = time.Now()
		sess.FileData = fileData
		sess.FileName = fileName

		if sess.Step == StepWaitingForFileToHide {
			sess.Step = StepWaitingForTextToHide
			sendCancelMessage(bot, chatID, "📝 Send the secret text you want to hide inside the file:")
		} else {
			encData, err := extractData(fileData)
			if err != nil {
				log.Printf("extraction error for chat %d: %v", chatID, err)
				msg := tgbotapi.NewMessage(chatID, "❌ Extraction failed: no hidden data found or the file is corrupted.")
				bot.Send(msg)
				sess.Step = StepIdle
				sendMainMenu(bot, chatID)
				return
			}
			sess.EncryptedData = encData
			sess.Step = StepWaitingForPassphraseToExtract
			sendCancelMessage(bot, chatID, "🔑 Enter the passphrase to decrypt the message:")
		}

	case StepWaitingForTextToHide:
		sess.TextToHide = update.Message.Text
		sess.Step = StepWaitingForPassphraseToHide
		sendCancelMessage(bot, chatID, fmt.Sprintf("🔒 Enter a passphrase to encrypt your message (minimum %d characters):", minPassphraseLength))

	case StepWaitingForPassphraseToHide:
		passphrase := update.Message.Text

		if len(passphrase) < minPassphraseLength {
			sendCancelMessage(bot, chatID, fmt.Sprintf("⚠️ Passphrase is too short! Please enter at least %d characters for strong protection:", minPassphraseLength))
			return
		}

		encryptedData, err := encryptText(sess.TextToHide, passphrase)
		sess.TextToHide = ""

		if err != nil {
			log.Printf("encryption error for chat %d: %v", chatID, err)
			msg := tgbotapi.NewMessage(chatID, "❌ An error occurred during encryption.")
			bot.Send(msg)
			sess.Step = StepIdle
			sendMainMenu(bot, chatID)
			return
		}

		finalFileData := embedData(sess.FileData, encryptedData)

		doc := tgbotapi.FileBytes{
			Name:  "encrypted_" + sess.FileName,
			Bytes: finalFileData,
		}
		fileMsg := tgbotapi.NewDocument(chatID, doc)
		fileMsg.Caption = "✅ Text hidden and encrypted successfully!\nYou can now send this file and the passphrase to your recipient."
		bot.Send(fileMsg)

		deleteMsg := tgbotapi.NewDeleteMessage(chatID, update.Message.MessageID)
		bot.Request(deleteMsg)

		resetSensitiveData(sess)
		sess.Step = StepIdle

	case StepWaitingForPassphraseToExtract:
		passphrase := update.Message.Text
		plainText, err := decryptText(sess.EncryptedData, passphrase)

		if err != nil {
			msg := tgbotapi.NewMessage(chatID, "❌ Incorrect passphrase or corrupted data!")
			bot.Send(msg)
		} else {
			msg := tgbotapi.NewMessage(chatID, "🔓 Secret Hidden Message:\n\n"+plainText)
			bot.Send(msg)
		}

		deleteMsg := tgbotapi.NewDeleteMessage(chatID, update.Message.MessageID)
		bot.Request(deleteMsg)

		resetSensitiveData(sess)
		sess.Step = StepIdle

	default:
		sendMainMenu(bot, chatID)
	}
}

func resetSensitiveData(sess *UserSession) {
	sess.FileData = nil
	sess.EncryptedData = nil
	sess.TextToHide = ""
	sess.FileName = ""
}

func sendMainMenu(bot *tgbotapi.BotAPI, chatID int64) {
	welcomeText := "Welcome to HidePass Bot 🔐\n\n" +
		"Easily embed password-protected text inside your files or extract hidden data securely.\n\n" +
		"Key Features:\n" +
		"• Encrypts messages with AES-256 standard\n" +
		"• Supports images, PDF, Word, and documents (<10MB)\n" +
		"• Privacy First: Files are processed instantly in memory and deleted immediately.\n\n" +
		"⚖️ Disclaimer: For personal privacy only. The creator is not responsible for misuse.\n\n" +
		"Choose an action to proceed:"

	msg := tgbotapi.NewMessage(chatID, welcomeText)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📥 Hide Text in File", "action_hide"),
			tgbotapi.NewInlineKeyboardButtonData("📤 Extract Text from File", "action_extract"),
		),
	)
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func sendCancelMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel Operation", "action_cancel"),
		),
	)
	msg.ReplyMarkup = keyboard
	bot.Send(msg)
}

func sendHelpMenu(bot *tgbotapi.BotAPI, chatID int64) {
	helpText := "💡 **HidePass Bot Help Guide**\n\n" +
		"1. **How to Hide Text:**\n" +
		"   • Click 'Hide Text in File'.\n" +
		"   • Send your file/image as an uncompressed Document (File).\n" +
		"   • Type your secret message and set a password (min 8 chars).\n" +
		"   • Download the generated file.\n\n" +
		"2. **How to Extract Text:**\n" +
		"   • Click 'Extract Text from File'.\n" +
		"   • Send the encrypted file as a Document.\n" +
		"   • Enter the correct password.\n\n" +
		"📌 **Note:** Always send images as 'Document' (uncompressed) to avoid loss of hidden data."

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, sess *UserSession) {
	chatID := query.Message.Chat.ID
	callback := tgbotapi.NewCallback(query.ID, "")
	bot.Request(callback)

	switch query.Data {
	case "action_hide":
		sess.Step = StepWaitingForFileToHide
		sendCancelMessage(bot, chatID, "📁 Send the file or image as an **uncompressed Document** (under 10MB):")
	case "action_extract":
		sess.Step = StepWaitingForFileToExtract
		sendCancelMessage(bot, chatID, "📁 Send the hidden file as a **Document**:")
	case "action_cancel":
		resetSensitiveData(sess)
		sess.Step = StepIdle
		msg := tgbotapi.NewMessage(chatID, "❌ Operation cancelled.")
		bot.Send(msg)
		sendMainMenu(bot, chatID)
	}
}

func downloadFile(bot *tgbotapi.BotAPI, fileID string, defaultName string) ([]byte, string, error) {
	fileURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		return nil, "", err
	}

	resp, err := httpClient.Get(fileURL)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, MaxFileSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, "", err
	}

	if len(data) > MaxFileSize {
		return nil, "", fmt.Errorf("file exceeds maximum allowed size")
	}

	if defaultName == "" {
		defaultName = "file.bin"
	}

	return data, defaultName, nil
}