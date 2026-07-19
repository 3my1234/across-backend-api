package services

import (
	"fmt"
	"net/smtp"

	"across/backend/internal/config"
)

type EmailService struct {
	cfg config.Config
}

func NewEmailService(cfg config.Config) *EmailService {
	return &EmailService{cfg: cfg}
}

func (e *EmailService) SendWelcomeEmail(toEmail, toName string) error {
	if e.cfg.SMTPHost == "" || e.cfg.SMTPUsername == "" {
		return nil
	}

	subject := "Welcome to Atlantic Express!"
	body := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"></head>
<body style="font-family:Arial,sans-serif;background:#F5F5F5;padding:20px;">
<div style="max-width:600px;margin:auto;background:#FFFFFF;border-radius:12px;padding:30px;">
<div style="text-align:center;margin-bottom:20px;">
<h1 style="color:#191919;font-size:24px;">Welcome to Atlantic Express!</h1>
</div>
<p style="color:#595959;font-size:16px;line-height:1.5;">Dear %s,</p>
<p style="color:#595959;font-size:16px;line-height:1.5;">Thank you for joining <strong>ATLANTIC SHANSU LOGISTICS LIMITED</strong>.</p>
<p style="color:#595959;font-size:16px;line-height:1.5;">You can now shop quality products from China, pay in Naira, and track your orders every step of the way.</p>
<div style="background:#FF4747;color:#FFFFFF;text-align:center;padding:12px;border-radius:8px;margin:20px 0;">
<a href="https://atlanticexpress.com" style="color:#FFFFFF;text-decoration:none;font-weight:bold;">Start Shopping</a>
</div>
<p style="color:#8C8C8C;font-size:12px;">ATLANTIC SHANSU LOGISTICS LIMITED</p>
</div></body></html>`, toName)

	msg := []byte("From: " + e.cfg.SMTPFromName + " <" + e.cfg.SMTPFromEmail + ">\r\n" +
		"To: " + toEmail + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n\r\n" + body)

	addr := e.cfg.SMTPHost + ":" + e.cfg.SMTPPort
	auth := smtp.PlainAuth("", e.cfg.SMTPUsername, e.cfg.SMTPPassword, e.cfg.SMTPHost)
	return smtp.SendMail(addr, auth, e.cfg.SMTPFromEmail, []string{toEmail}, msg)
}
