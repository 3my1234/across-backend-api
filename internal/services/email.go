package services

import (
	"fmt"
	"html"
	"mime/multipart"
	"net/smtp"
	"net/textproto"
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
	plainName := strings.TrimSpace(toName)
	if plainName == "" {
		plainName = "there"
	}
	name := html.EscapeString(plainName)
	link := html.EscapeString(strings.TrimSpace(verificationURL))
	body := e.layout("Verify your email", "Confirm your email address to activate your Atlantic Express account.", fmt.Sprintf(`
<p style="margin:0 0 16px;color:#30423D;font-size:16px;line-height:1.6;">Hello %s,</p>
<p style="margin:0 0 18px;color:#30423D;font-size:16px;line-height:1.6;">Confirm your email address to activate your Atlantic Express account.</p>
<p style="margin:24px 0;text-align:center;"><a href="%s" style="display:inline-block;background:#FF4747;color:#FFFFFF;text-decoration:none;font-weight:700;padding:14px 28px;border-radius:8px;">Verify email address</a></p>
<p style="margin:0 0 8px;color:#66736F;font-size:13px;line-height:1.5;">This secure link expires in 24 hours. If you did not create this account, you can safely ignore this email.</p>
<p style="margin:18px 0 0;color:#7B8783;font-size:12px;line-height:1.5;word-break:break-all;">Button not working? Copy and paste this link into your browser:<br><a href="%s" style="color:#0F6B5A;">%s</a></p>`, name, link, link, link))
	plain := fmt.Sprintf("Hello %s,\n\nConfirm your email address to activate your Atlantic Express account:\n%s\n\nThis secure link expires in 24 hours. If you did not create this account, you can safely ignore this email.", plainName, strings.TrimSpace(verificationURL))
	return e.SendHTMLWithText(toEmail, "Verify your Atlantic Express email", plain, body)
}

func (e *EmailService) SendWelcomeEmail(toEmail, toName string) error {
	plainName := strings.TrimSpace(toName)
	if plainName == "" {
		plainName = "there"
	}
	name := html.EscapeString(plainName)
	website := html.EscapeString(strings.TrimSpace(e.cfg.WebsiteURL))
	body := e.layout("Welcome to Atlantic Express", "Your Atlantic Express account is ready.", fmt.Sprintf(`
<p style="margin:0 0 16px;color:#30423D;font-size:16px;line-height:1.6;">Hello %s,</p>
<p style="margin:0 0 16px;color:#30423D;font-size:16px;line-height:1.6;">Your Atlantic Express account is ready. You also received <strong>100 XP</strong>, worth <strong>&#8358;100</strong> in discounts.</p>
<p style="margin:0 0 18px;color:#30423D;font-size:16px;line-height:1.6;">Shop international products, pay securely in Naira, and follow delivery progress from purchase to arrival.</p>
<table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="margin:22px 0;background:#F3F7F6;border:1px solid #DDE9E5;border-radius:12px;"><tr>
<td style="padding:16px;text-align:center;color:#0F3D35;font-size:14px;line-height:1.45;"><strong>Browse</strong><br>Curated products</td>
<td style="padding:16px;text-align:center;color:#0F3D35;font-size:14px;line-height:1.45;border-left:1px solid #DDE9E5;"><strong>Pay</strong><br>Securely in Naira</td>
<td style="padding:16px;text-align:center;color:#0F3D35;font-size:14px;line-height:1.45;border-left:1px solid #DDE9E5;"><strong>Track</strong><br>Every delivery stage</td>
</tr></table>
<p style="margin:24px 0 4px;text-align:center;"><a href="%s" style="display:inline-block;background:#0F3D35;color:#FFFFFF;text-decoration:none;font-weight:700;padding:14px 28px;border-radius:8px;">Explore Atlantic Express</a></p>`, name, website))
	plain := fmt.Sprintf("Hello %s,\n\nWelcome to Atlantic Express. Your account is ready, and you received 100 XP worth N100 in discounts.\n\nShop international products, pay securely in Naira, and track every delivery stage.\n\n%s", plainName, strings.TrimSpace(e.cfg.WebsiteURL))
	return e.SendHTMLWithText(toEmail, "Welcome to Atlantic Express", plain, body)
}

func (e *EmailService) layout(title, preheader, content string) string {
	logo := `<div style="font-size:22px;font-weight:800;color:#0F3D35;">Atlantic Express</div>`
	if value := strings.TrimSpace(e.cfg.BrandLogoURL); value != "" {
		logo = `<img src="` + html.EscapeString(value) + `" width="190" alt="Atlantic Express" style="display:block;max-width:190px;width:100%;height:auto;border:0;">`
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light"><title>` + html.EscapeString(title) + `</title></head>
<body style="margin:0;padding:0;background:#F3F7F6;font-family:Arial,Helvetica,sans-serif;-webkit-text-size-adjust:100%;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">` + html.EscapeString(preheader) + `</div>
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="width:100%;background:#F3F7F6;"><tr><td align="center" style="padding:28px 12px;">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="width:100%;max-width:600px;background:#FFFFFF;border-radius:16px;overflow:hidden;border:1px solid #E2EBE8;box-shadow:0 8px 24px rgba(15,61,53,.08);">
<tr><td style="height:5px;background:#FF4747;font-size:0;line-height:0;">&nbsp;</td></tr>
<tr><td align="center" style="padding:24px 32px 20px;background:#F8FBFA;border-bottom:1px solid #E2EBE8;">` + logo + `<div style="margin-top:8px;color:#516660;font-size:12px;letter-spacing:.8px;text-transform:uppercase;">From China to Africa, delivering possibilities</div></td></tr>
<tr><td style="padding:32px;"><h1 style="margin:0 0 20px;color:#142522;font-size:25px;line-height:1.25;">` + html.EscapeString(title) + `</h1>` + content + `</td></tr>
<tr><td style="padding:22px 32px;background:#0F3D35;color:#DCEAE6;font-size:12px;line-height:1.6;"><strong style="color:#FFFFFF;">ATLANTIC SHANSU LOGISTICS LIMITED</strong><br>Procurement and supply-chain logistics.<br><span style="color:#AFC8C1;">This is an automated service email. Reply to reach our support team.</span></td></tr>
</table></td></tr></table></body></html>`
}

func (e *EmailService) SendHTML(toEmail, subject, htmlBody string) error {
	return e.SendHTMLWithText(toEmail, subject, "", htmlBody)
}

func (e *EmailService) SendHTMLWithText(toEmail, subject, textBody, htmlBody string) error {
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
		"Auto-Submitted: auto-generated",
	}
	if replyTo := strings.TrimSpace(e.cfg.SMTPReplyTo); replyTo != "" {
		headers = append(headers, "Reply-To: "+replyTo)
	}
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	headers = append(headers, "MIME-Version: 1.0", "Content-Type: multipart/alternative; boundary="+writer.Boundary())
	plainHeader := textproto.MIMEHeader{}
	plainHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	plainHeader.Set("Content-Transfer-Encoding", "8bit")
	plainPart, err := writer.CreatePart(plainHeader)
	if err != nil {
		return err
	}
	if strings.TrimSpace(textBody) == "" {
		textBody = "Please view this message in an HTML-capable email client."
	}
	if _, err := plainPart.Write([]byte(textBody)); err != nil {
		return err
	}
	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "8bit")
	htmlPart, err := writer.CreatePart(htmlHeader)
	if err != nil {
		return err
	}
	if _, err := htmlPart.Write([]byte(htmlBody)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	message := []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body.String())
	address := e.cfg.SMTPHost + ":" + e.cfg.SMTPPort
	auth := smtp.PlainAuth("", e.cfg.SMTPUsername, e.cfg.SMTPPassword, e.cfg.SMTPHost)
	return smtp.SendMail(address, auth, e.cfg.SMTPFromEmail, []string{toEmail}, message)
}
