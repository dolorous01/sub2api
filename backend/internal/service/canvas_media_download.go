package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type stagedCanvasMedia struct {
	path        string
	size        int64
	contentType string
}

func (s *stagedCanvasMedia) Open() (*os.File, error) {
	if s == nil || s.path == "" {
		return nil, fmt.Errorf("canvas media staging file is unavailable")
	}
	return os.Open(s.path)
}

func (s *stagedCanvasMedia) Cleanup() {
	if s != nil && s.path != "" {
		_ = os.Remove(s.path)
		s.path = ""
	}
}

func stageCanvasMediaReader(ctx context.Context, reader io.Reader, declaredSize, maxBytes int64, contentType string) (*stagedCanvasMedia, error) {
	if reader == nil || maxBytes <= 0 || declaredSize > maxBytes {
		return nil, fmt.Errorf("canvas media response size is invalid")
	}
	file, err := os.CreateTemp("", "sub2api-generated-media-*")
	if err != nil {
		return nil, fmt.Errorf("create generated media staging file: %w", err)
	}
	path := file.Name()
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	written, err := io.Copy(file, io.LimitReader(&canvasContextReader{ctx: ctx, reader: reader}, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("stage generated media: %w", err)
	}
	if written <= 0 || written > maxBytes || (declaredSize >= 0 && declaredSize != written) {
		return nil, fmt.Errorf("generated media response size is invalid")
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	cleanup = false
	return &stagedCanvasMedia{path: path, size: written, contentType: strings.TrimSpace(contentType)}, nil
}

func downloadCanvasMediaURL(ctx context.Context, rawURL string, maxBytes int64) (*stagedCanvasMedia, error) {
	if err := validateCanvasMediaResultURL(rawURL); err != nil {
		return nil, err
	}
	client := newCanvasMediaDownloadClient()
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("canvas media download returned status %d", response.StatusCode)
	}
	return stageCanvasMediaReader(ctx, response.Body, response.ContentLength, maxBytes, response.Header.Get("Content-Type"))
}

func newCanvasMediaDownloadClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublicCanvasMedia(ctx, network, address, net.DefaultResolver.LookupIPAddr, dialer.DialContext)
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many canvas media redirects")
			}
			return validateCanvasMediaResultURL(request.URL.String())
		},
	}
}

func dialPublicCanvasMedia(
	ctx context.Context,
	network, address string,
	lookup func(context.Context, string) ([]net.IPAddr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	public := 0
	var dialErrors []error
	for _, candidate := range addresses {
		if !publicCanvasMediaIP(candidate.IP) {
			continue
		}
		public++
		candidateAddress := net.JoinHostPort(candidate.IP.String(), port)
		connection, dialErr := dial(ctx, network, candidateAddress)
		if dialErr == nil {
			return connection, nil
		}
		dialErrors = append(dialErrors, fmt.Errorf("%s: %w", candidateAddress, dialErr))
	}
	if public == 0 {
		return nil, fmt.Errorf("canvas media host has no public address")
	}
	return nil, fmt.Errorf("connect to canvas media host: %w", errors.Join(dialErrors...))
}

func validateCanvasMediaResultURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "https") ||
		parsed.Hostname() == "" || parsed.User != nil || isBlockedHostname(parsed.Hostname()) {
		return fmt.Errorf("canvas media result URL is not a public HTTPS URL")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return fmt.Errorf("canvas media result URL port is invalid")
		}
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !publicCanvasMediaIP(ip) {
		return fmt.Errorf("canvas media result URL points to a non-public address")
	}
	return nil
}

func publicCanvasMediaIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !isPrivateIP(ip)
}
