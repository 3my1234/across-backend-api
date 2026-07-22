package services

import (
	"fmt"
	"html"
	"net/smtp"
	"strings"
	"time"

	"across/backend/internal/config"
)

type EmailService struct {
	cfg config.Config
}

func NewEmailService(cfg config.Config) *EmailService {
	return &EmailService{cfg: cfg}
}

func (e *EmailService) SendVerificationEmail(toEmail, toName, verificationURL string) error {
	name := html.EscapeString(strings.TrimSpace(toName))
	if name == "" {
		name = "there"
	}
	body := e.layout("Verify your email", fmt.Sprintf(`
<p style="margin:0 0 16px;color:#30423D;font-size:16px;line-height:1.6;">Hello %s,</p>
<p style="margin:0 0 18px;color:#30423D;font-size:16px;line-height:1.6;">Confirm your email address to activate your Atlantic Express account.</p>
<p style="margin:24px 0;text-align:center;"><a href="%s" style="display:inline-block;background:#FF4747;color:#FFFFFF;text-decoration:none;font-weight:700;padding:14px 28px;border-radius:8px;">Verify email address</a></p>
<p style="margin:0 0 8px;color:#66736F;font-size:13px;line-height:1.5;">This secure link expires in 24 hours. If you did not create this account, you can ignore this email.</p>`, name, html.EscapeString(verificationURL)))
	return e.SendHTML(toEmail, "Verify your Atlantic Express email", body)
}

func (e *EmailService) SendWelcomeEmail(toEmail, toName string) error {
	name := html.EscapeString(strings.TrimSpace(toName))
	if name == "" {
		name = "there"
	}
	website := html.EscapeString(strings.TrimSpace(e.cfg.WebsiteURL))
	body := e.layout("Welcome to Atlantic Express", fmt.Sprintf(`
<p style="margin:0 0 16px;color:#30423D;font-size:16px;line-height:1.6;">Hello %s,</p>
<p style="margin:0 0 16px;color:#30423D;font-size:16px;line-height:1.6;">Your Atlantic Express account is ready. You also received <strong>100 XP</strong>, worth <strong>N100</strong> in discounts.</p>
<p style="margin:0 0 18px;color:#30423D;font-size:16px;line-height:1.6;">Shop international products, pay securely in Naira, and follow delivery progress from purchase to arrival.</p>
<p style="margin:24px 0;text-align:center;"><a href="%s" style="display:inline-block;background:#0F3D35;color:#FFFFFF;text-decoration:none;font-weight:700;padding:14px 28px;border-radius:8px;">Visit Atlantic Express</a></p>`, name, website))
	return e.SendHTML(toEmail, "Welcome to Atlantic Express", body)
}

func (e *EmailService) layout(title, content string) string {
	logo := `<div style="font-size:22px;font-weight:800;color:#0F3D35;">Atlantic Express</div>`
	if value := strings.TrimSpace(e.cfg.BrandLogoURL); value != "" {
		logo = `<img src="` + html.EscapeString(value) + `" width="160" alt="Atlantic Express" style="display:block;max-width:160px;height:auto;border:0;">`
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#F3F7F6;font-family:Arial,Helvetica,sans-serif;">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="background:#F3F7F6;padding:28px 12px;"><tr><td align="center">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="max-width:600px;background:#FFFFFF;border-radius:16px;overflow:hidden;border:1px solid #E2EBE8;">
<tr><td style="padding:28px 32px;background:#F8FBFA;border-bottom:1px solid #E2EBE8;">` + logo + `</td></tr>
<tr><td style="padding:32px;"><h1 style="margin:0 0 20px;color:#142522;font-size:25px;line-height:1.25;">` + html.EscapeString(title) + `</h1>` + content + `</td></tr>
<tr><td style="padding:20px 32px;background:#0F3D35;color:#DCEAE6;font-size:12px;line-height:1.5;">ATLANTIC SHANSU LOGISTICS LIMITED<br>Procurement and supply-chain logistics.</td></tr>
</table></td></tr></table></body></html>`
}

func (e *EmailService) SendHTML(toEmail, subject, htmlBody string) error {
	if strings.TrimSpace(e.cfg.SMTPHost) == "" || strings.TrimSpace(e.cfg.SMTPUsername) == "" {
		return nil
	}
	subject = strings.ReplaceAll(strings.ReplaceAll(subject, "\r", ""), "\n", "")
	fromName := strings.ReplaceAll(strings.ReplaceAll(e.cfg.SMTPFromName, "\r", ""), "\n", "")
	headers := []string{
		"From: " + fromName + " <" + e.cfg.SMTPFromEmail + ">",
		"To: " + toEmail,
		"Subject: " + subject,
		"Date: " + time.Now().Format(time.RFC1123Z),
		fmt.Sprintf("Message-ID: <%d.%s@%s>", time.Now().UnixNano(), strings.ReplaceAll(toEmail, "@", "."), strings.TrimPrefix(e.cfg.SMTPFromEmail[strings.LastIndex(e.cfg.SMTPFromEmail, "@")+1:], "@")),
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"Auto-Submitted: auto-generated",
	}
	if replyTo := strings.TrimSpace(e.cfg.SMTPReplyTo); replyTo != "" {
		headers = append(headers, "Reply-To: "+replyTo)
	}
	message := []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + htmlBody)
	address := e.cfg.SMTPHost + ":" + e.cfg.SMTPPort
	auth := smtp.PlainAuth("", e.cfg.SMTPUsername, e.cfg.SMTPPassword, e.cfg.SMTPHost)
	return smtp.SendMail(address, auth, e.cfg.SMTPFromEmail, []string{toEmail}, message)
}
