package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Checker struct {
	Client    *http.Client
	ReqHeader http.Header
}

func (c Checker) Check(ctx context.Context, url string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	req.Header = c.ReqHeader

	rsp, err := c.Client.Do(req)
	if err != nil {
		return false, fmt.Errorf("perform request: %w", err)
	}
	defer rsp.Body.Close()

	_, err = io.Copy(io.Discard, rsp.Body)
	if err != nil {
		return false, fmt.Errorf("read body: %w", err)
	}

	return checkHeaders(
		url,
		rsp.Header,
		map[string]string{
			"Access-Control-Allow-Methods": "GET",
			"Access-Control-Allow-Origin":  "*",
		},
	), nil
}

func checkHeaders(url string, h http.Header, expect map[string]string) bool {
	ok := true
	for name, val := range expect {
		actual := h.Get(name)
		if actual != val {
			slog.Error("header mismatch", "url", url, "header", name, "expected", val, "got", actual)
			ok = false
		}
	}

	return ok
}

func loadJSON(path string, data any) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return json.Unmarshal(buf, &data)
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
	reqheaderfile := flag.String("reqheaders", "", "path to JSON file with request headers")
	urlfile := flag.String("urls", "", "path to file with list of URLs to check")
	flag.Parse()
	if *urlfile == "" {
		flag.Usage()
		os.Exit(2)
	}

	var reqheader http.Header
	if *reqheaderfile != "" {
		err := loadJSON(*reqheaderfile, &reqheader)
		if err != nil {
			return fmt.Errorf("load request headers: %w", err)
		}
	}

	urls, err := loadURLs(*urlfile)
	if err != nil {
		return fmt.Errorf("load URLs: %w", err)
	}

	checker := Checker{
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
		ReqHeader: reqheader,
	}

	var wg sync.WaitGroup
	wg.Add(len(urls))

	var hadError atomic.Bool
	for _, url := range urls {
		go func() {
			defer wg.Done()
			ok, err := checker.Check(ctx, url)
			if err != nil {
				slog.Error("check URL", "url", url, "err", err)
			}
			if !ok || err != nil {
				hadError.Store(true)
			}
		}()
	}

	wg.Wait()
	if hadError.Load() {
		return errors.New("unsuccessful")
	}
	return nil
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
