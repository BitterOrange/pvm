package qemu

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
	"time"
)

// downloadClient has no total timeout: a 15 MB build on a slow link must
// finish. A stall is caught by the idle timeout on every read instead.
func downloadClient() (*http.Client, error) {
	proxy, err := proxyFunc()
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: tr}, nil
}

// proxyFunc uses HTTPS_PROXY/HTTP_PROXY, and on Windows, when those are
// unset, the static proxy from the Internet settings. A proxy script (PAC)
// cannot be followed and is an error naming the ways around it.
func proxyFunc() (func(*http.Request) (*url.URL, error), error) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if os.Getenv(k) != "" {
			return http.ProxyFromEnvironment, nil
		}
	}
	server, pac := systemProxy()
	if server != "" {
		u, err := url.Parse("http://" + server)
		if err != nil {
			return nil, fmt.Errorf("the system proxy %q is not host:port; set HTTPS_PROXY", server)
		}
		return http.ProxyURL(u), nil
	}
	if pac {
		return nil, errors.New("Windows uses a proxy script (PAC) that pvm cannot read; set HTTPS_PROXY, or download the archive yourself and run pvm qemu install --file <archive>")
	}
	return nil, nil
}

// idleReader fails a read that delivers nothing for the idle timeout.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.timer.Reset(60 * time.Second)
	return n, err
}

// download fetches url into dest, resuming dest+".part" with HTTP Range,
// retrying three times, and refusing more than size bytes.
func download(ctx context.Context, rawURL, dest string, size int64, progress func(done, total int64)) error {
	client, err := downloadClient()
	if err != nil {
		return err
	}
	part := dest + ".part"
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
			}
		}
		lastErr = downloadOnce(ctx, client, rawURL, part, size, progress)
		if lastErr == nil {
			return os.Rename(part, dest)
		}
		var perm permanentError
		if errors.As(lastErr, &perm) {
			return lastErr
		}
	}
	return fmt.Errorf("download %s: %w; check the network or proxy, or download it yourself and use --file", rawURL, lastErr)
}

type permanentError struct{ error }

func downloadOnce(ctx context.Context, client *http.Client, rawURL, part string, size int64, progress func(done, total int64)) error {
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	if size > 0 && have > size {
		os.Remove(part)
		have = 0
	}
	if size > 0 && have == size {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return permanentError{err}
	}
	req.Header.Set("User-Agent", "pvm")
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
	case resp.StatusCode == http.StatusOK:
		have = 0 // the server ignored the range: start over
	case resp.StatusCode == http.StatusNotFound:
		return permanentError{fmt.Errorf("%s: not found (404)", rawURL)}
	default:
		return fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if have == 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return permanentError{err}
	}
	defer f.Close()
	timer := time.AfterFunc(60*time.Second, cancel)
	defer timer.Stop()
	body := io.Reader(&idleReader{r: resp.Body, timer: timer})
	if size > 0 {
		body = io.LimitReader(body, size-have+1)
	}
	buf := make([]byte, 256<<10)
	done := have
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return permanentError{err}
			}
			done += int64(n)
			if size > 0 && done > size {
				return permanentError{fmt.Errorf("%s is larger than the pinned %d bytes", rawURL, size)}
			}
			if progress != nil {
				progress(done, size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("no data for 60 s")
			}
			return rerr
		}
	}
	if size > 0 && done != size {
		return fmt.Errorf("got %d of %d bytes", done, size)
	}
	return f.Sync()
}
