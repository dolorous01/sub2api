package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

func TestValidateCanvasMediaResultURL(t *testing.T) {
	for _, rawURL := range []string{
		"https://example.com/video.mp4",
		"https://1.1.1.1/video.mp4",
	} {
		if err := validateCanvasMediaResultURL(rawURL); err != nil {
			t.Fatalf("validateCanvasMediaResultURL(%q) error = %v", rawURL, err)
		}
	}

	for _, rawURL := range []string{
		"http://example.com/video.mp4",
		"https://user:secret@example.com/video.mp4",
		"https://localhost/video.mp4",
		"https://metadata.google.internal/video.mp4",
		"https://127.0.0.1/video.mp4",
		"https://10.0.0.1/video.mp4",
		"https://169.254.169.254/latest/meta-data",
		"https://100.64.0.1/video.mp4",
		"https://192.0.2.1/video.mp4",
		"https://198.18.0.1/video.mp4",
		"https://198.51.100.1/video.mp4",
		"https://203.0.113.1/video.mp4",
		"https://240.0.0.1/video.mp4",
		"https://[::1]/video.mp4",
		"https://[64:ff9b::7f00:1]/video.mp4",
		"https://[2001:db8::1]/video.mp4",
		"https://[2002:7f00:1::]/video.mp4",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if err := validateCanvasMediaResultURL(rawURL); err == nil {
				t.Fatalf("validateCanvasMediaResultURL(%q) succeeded", rawURL)
			}
		})
	}
}

func TestCanvasMediaDownloadClientDoesNotUseEnvironmentProxy(t *testing.T) {
	client := newCanvasMediaDownloadClient()
	t.Cleanup(client.CloseIdleConnections)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("canvas media downloads must not use an environment proxy")
	}
}

func TestDialPublicCanvasMediaTriesAllPublicAddresses(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	attempts := make([]string, 0, 2)
	connection, err := dialPublicCanvasMedia(
		context.Background(),
		"tcp",
		"media.example:443",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.ParseIP("1.1.1.1")},
				{IP: net.ParseIP("8.8.8.8")},
			}, nil
		},
		func(_ context.Context, _, address string) (net.Conn, error) {
			attempts = append(attempts, address)
			if len(attempts) == 1 {
				return nil, errors.New("first address unavailable")
			}
			return client, nil
		},
	)
	if err != nil {
		t.Fatalf("dialPublicCanvasMedia() error = %v", err)
	}
	if connection != client || len(attempts) != 2 {
		t.Fatalf("dialPublicCanvasMedia() connection = %T, attempts = %v", connection, attempts)
	}
}
