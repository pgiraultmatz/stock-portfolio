package main

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
)

type Mailer interface {
	SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error
}

// GmailMailer sends emails via Gmail SMTP using an App Password.
type GmailMailer struct {
	from     string
	password string
}

func NewGmailMailer(from, password string) *GmailMailer {
	return &GmailMailer{from: from, password: password}
}

func (m *GmailMailer) SendVerificationEmail(_ context.Context, to, name, verifyURL string) error {
	auth := smtp.PlainAuth("", m.from, m.password, "smtp.gmail.com")
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="margin:0;padding:0;background:#f4f4f5;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif">
  <table width="100%%" cellpadding="0" cellspacing="0" style="padding:40px 0">
    <tr><td align="center">
      <table width="480" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:8px;overflow:hidden;border:1px solid #e4e4e7">

        <!-- Header -->
        <tr>
          <td style="background:#0f172a;padding:28px 40px">
            <p style="margin:0;font-size:18px;font-weight:700;color:#ffffff;letter-spacing:-0.3px">Stock Portfolio</p>
          </td>
        </tr>

        <!-- Body -->
        <tr>
          <td style="padding:40px">
            <p style="margin:0 0 8px;font-size:22px;font-weight:700;color:#0f172a">Confirm your email address</p>
            <p style="margin:0 0 28px;font-size:15px;color:#64748b;line-height:1.6">Hi %s, thanks for signing up. Click the button below to verify your email and activate your account.</p>

            <table cellpadding="0" cellspacing="0">
              <tr>
                <td style="background:#0f172a;border-radius:6px">
                  <a href="%s" style="display:inline-block;padding:13px 28px;font-size:14px;font-weight:600;color:#ffffff;text-decoration:none">Confirm my account</a>
                </td>
              </tr>
            </table>

            <p style="margin:28px 0 0;font-size:13px;color:#94a3b8;line-height:1.6">If the button doesn't work, copy and paste this link into your browser:<br>
              <a href="%s" style="color:#3b82f6;word-break:break-all">%s</a>
            </p>
          </td>
        </tr>

        <!-- Footer -->
        <tr>
          <td style="padding:20px 40px;border-top:1px solid #f1f5f9">
            <p style="margin:0;font-size:12px;color:#94a3b8">This link expires in 24 hours. If you didn't create an account, you can safely ignore this email.</p>
          </td>
        </tr>

      </table>
    </td></tr>
  </table>
</body>
</html>`, name, verifyURL, verifyURL, verifyURL)

	msg := fmt.Sprintf("From: Stock Portfolio <%s>\r\nTo: %s\r\nSubject: Confirm your account\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s",
		m.from, to, html)
	return smtp.SendMail("smtp.gmail.com:587", auth, m.from, []string{to}, []byte(msg))
}

// LogMailer logs verification links to stdout — used when GMAIL_APP_PASSWORD is not set.
type LogMailer struct{}

func (m *LogMailer) SendVerificationEmail(_ context.Context, to, name, verifyURL string) error {
	log.Printf("[MAIL] Verification link for %s (%s): %s", name, to, verifyURL)
	return nil
}
