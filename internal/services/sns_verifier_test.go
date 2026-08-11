package services

import (
	"strings"
	"testing"
)

func TestSNSCanonicalNotification(t *testing.T) {
	message := SNSMessage{
		Type: "Notification", MessageID: "message-id", Message: "payload", Subject: "subject",
		Timestamp: "2026-08-11T10:00:00.000Z", TopicARN: "arn:aws:sns:us-east-1:123456789012:ses-events",
	}
	canonical, err := snsCanonicalString(message)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Message\npayload\nMessageId\nmessage-id\nSubject\nsubject\nTimestamp\n2026-08-11T10:00:00.000Z\nTopicArn\narn:aws:sns:us-east-1:123456789012:ses-events\nType\nNotification\n"
	if canonical != expected {
		t.Fatalf("unexpected canonical message:\n%s", canonical)
	}
}

func TestValidateSNSURLRejectsUntrustedOrUnsafeURLs(t *testing.T) {
	for _, candidate := range []string{
		"http://sns.us-east-1.amazonaws.com/cert.pem",
		"https://sns.us-east-1.amazonaws.com.evil.example/SimpleNotificationService-test.pem",
		"https://user@sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem",
		"https://sns.us-east-1.amazonaws.com:443/SimpleNotificationService-test.pem",
		"https://sns.us-east-1.amazonaws.com/not-a-certificate.pem",
	} {
		if _, err := validateSNSURL(candidate, true); err == nil {
			t.Fatalf("expected %q to be rejected", candidate)
		}
	}
}

func TestValidateSNSURLAcceptsAWSRegionalSNSCertificate(t *testing.T) {
	parsed, err := validateSNSURL("https://sns.us-east-1.amazonaws.com/SimpleNotificationService-abc123.pem", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(parsed.Hostname(), "sns.us-east-1.amazonaws.com") {
		t.Fatalf("unexpected host %q", parsed.Hostname())
	}
}
