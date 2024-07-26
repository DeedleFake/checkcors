package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

type Checker struct {
	Client *http.Client
	Origin string
}

func (c Checker) Check(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if c.Origin != "" {
		req.Header.Set("Origin", c.Origin)
	}

	rsp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer rsp.Body.Close()

	_, err = io.Copy(io.Discard, rsp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	return checkHeaders(
		rsp.Header,
		map[string]string{
			"Access-Control-Allowed-Methods": "GET",
			"Access-Control-Allow-Origin":    "*",
		},
	)
}

func checkHeaders(h http.Header, expect map[string]string) error {
	var errs []error
	for name, val := range expect {
		actual := h.Get(name)
		if actual != val {
			errs = append(errs, fmt.Errorf("expected header %q to be %q but was %q", name, val, actual))
		}
	}

	return errors.Join(errs...)
}

func loadURLs(path string) (urls []string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	s := bufio.NewScanner(file)
	for s.Scan() {
		line := s.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed[0] == '#' {
			continue
		}

		urls = append(urls, line)
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	return urls, nil
}

func run(ctx context.Context) error {
	origin := flag.String("origin", "", "Origin header to send")
	urlfile := flag.String("urls", "", "path to file with list of URLs to check")
	flag.Parse()
	if *urlfile == "" {
		flag.Usage()
		os.Exit(2)
	}

	urls, err := loadURLs(*urlfile)
	if err != nil {
		return fmt.Errorf("load URLs: %w", err)
	}

	checker := Checker{
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
		Origin: *origin,
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, url := range urls {
		eg.Go(func() error {
			err := checker.Check(ctx, url)
			if err != nil {
				return fmt.Errorf("check %q: %w", url, err)
			}
			return nil
		})
	}
	return eg.Wait()
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	err := run(ctx)
	if err != nil {
		slog.Error("failed", "err", err)
		os.Exit(1)
	}
}
