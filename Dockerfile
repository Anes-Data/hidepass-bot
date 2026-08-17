# 1. استخدام بيئة Go الرسمية لبناء الكود (Build Stage)
FROM golang:1.21-alpine AS builder

# تحديد مجلد العمل داخل الحاوية
WORKDIR /app

# نسخ ملفات إدارة المكتبات أولاً لتسريع عملية البناء
COPY go.mod go.sum ./
RUN go mod download

# نسخ باقي ملفات المشروع
COPY . .

# بناء الملف التنفيذي للغة Go
RUN CGO_ENABLED=0 GOOS=linux go build -o bot .

# 2. مرحلة التشغيل الخفيفة (Run Stage)
FROM alpine:latest

WORKDIR /app

# تثبيت شهادات الأمان لتشغيل الاتصال المشفر بـ Telegram
RUN apk --no-cache add ca-certificates

# نسخ الملف التنفيذي فقط من مرحلة البناء الأولى
COPY --from=builder /app/bot .

# الأمر الذي سيتم تشغيله عند بدء الحاوية
CMD ["./bot"]