package services

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type SNSMessage struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicARN         string `json:"TopicArn"`
	Subject          string `json:"Subject"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	SubscribeURL     string `json:"SubscribeURL"`
	Token            string `json:"Token"`
}

type SNSVerifier struct {
	client *http.Client
	cache  sync.Map
}

func NewSNSVerifier() *SNSVerifier {
	return &SNSVerifier{client: &http.Client{Timeout: 8 * time.Second}}
}

func (v *SNSVerifier) Verify(ctx context.Context, message SNSMessage, expectedTopicARN string) error {
	if strings.TrimSpace(expectedTopicARN) == "" || message.TopicARN != expectedTopicARN {
		return errors.New("unexpected SNS topic")
	}
	if message.SignatureVersion != "1" && message.SignatureVersion != "2" {
		return errors.New("unsupported SNS signature version")
	}
	certificateURL, err := validateSNSURL(message.SigningCertURL, true)
	if err != nil {
		return err
	}
	certificate, err := v.certificate(ctx, certificateURL.String())
	if err != nil {
		return err
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("SNS certificate does not contain an RSA public key")
	}
	signature, err := base64.StdEncoding.DecodeString(message.Signature)
	if err != nil {
		return errors.New("invalid SNS signature encoding")
	}
	canonical, err := snsCanonicalString(message)
	if err != nil {
		return err
	}
	if message.SignatureVersion == "2" {
		digest := sha256.Sum256([]byte(canonical))
		return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature)
	}
	digest := sha1.Sum([]byte(canonical)) // SNS signature version 1 requires SHA-1.
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA1, digest[:], signature)
}

func (v *SNSVerifier) ConfirmSubscription(ctx context.Context, subscribeURL string) error {
	validated, err := validateSNSURL(subscribeURL, false)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, validated.String(), nil)
	if err != nil {
		return err
	}
	response, err := v.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("SNS subscription confirmation HTTP %d", response.StatusCode)
	}
	return nil
}

func (v *SNSVerifier) certificate(ctx context.Context, certificateURL string) (*x509.Certificate, error) {
	if cached, ok := v.cache.Load(certificateURL); ok {
		return cached.(*x509.Certificate), nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, certificateURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := v.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SNS certificate HTTP %d", response.StatusCode)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(encoded)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("invalid SNS certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if time.Now().Before(certificate.NotBefore) || time.Now().After(certificate.NotAfter) {
		return nil, errors.New("SNS certificate is outside its validity period")
	}
	v.cache.Store(certificateURL, certificate)
	return certificate, nil
}

func validateSNSURL(rawURL string, certificate bool) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return nil, errors.New("invalid SNS URL")
	}
	host := strings.ToLower(parsed.Hostname())
	validHost := strings.HasPrefix(host, "sns.") && (strings.HasSuffix(host, ".amazonaws.com") || strings.HasSuffix(host, ".amazonaws.com.cn"))
	if !validHost {
		return nil, errors.New("SNS URL host is not trusted")
	}
	if certificate && (!strings.HasPrefix(parsed.Path, "/SimpleNotificationService-") || !strings.HasSuffix(parsed.Path, ".pem")) {
		return nil, errors.New("invalid SNS certificate path")
	}
	return parsed, nil
}

func snsCanonicalString(message SNSMessage) (string, error) {
	fields := make([][2]string, 0, 8)
	switch message.Type {
	case "Notification":
		fields = append(fields, [2]string{"Message", message.Message}, [2]string{"MessageId", message.MessageID})
		if message.Subject != "" {
			fields = append(fields, [2]string{"Subject", message.Subject})
		}
		fields = append(fields, [2]string{"Timestamp", message.Timestamp}, [2]string{"TopicArn", message.TopicARN}, [2]string{"Type", message.Type})
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		fields = append(fields,
			[2]string{"Message", message.Message},
			[2]string{"MessageId", message.MessageID},
			[2]string{"SubscribeURL", message.SubscribeURL},
			[2]string{"Timestamp", message.Timestamp},
			[2]string{"Token", message.Token},
			[2]string{"TopicArn", message.TopicARN},
			[2]string{"Type", message.Type},
		)
	default:
		return "", errors.New("unsupported SNS message type")
	}
	var canonical strings.Builder
	for _, field := range fields {
		if field[1] == "" {
			return "", fmt.Errorf("SNS field %s is required", field[0])
		}
		canonical.WriteString(field[0])
		canonical.WriteByte('\n')
		canonical.WriteString(field[1])
		canonical.WriteByte('\n')
	}
	return canonical.String(), nil
}
