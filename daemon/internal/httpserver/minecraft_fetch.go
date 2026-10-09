package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

func fetchJSON(r *http.Request, requestURL string, target any) error {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New(requestURL + " returned " + strconv.Itoa(response.StatusCode))
	}
	return decodeBoundedJSON(response.Body, target)
}

func fetchText(r *http.Request, requestURL string) (string, error) {
	response, err := fetchResponse(r, requestURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New(requestURL + " returned " + strconv.Itoa(response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxExternalResponseBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxExternalResponseBytes {
		return "", errors.New("External response is too large")
	}
	return string(data), nil
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	limited := &io.LimitedReader{R: reader, N: maxExternalResponseBytes + 1}
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		if limited.N <= 0 {
			return errors.New("External response is too large")
		}
		return err
	}
	if limited.N <= 0 {
		return errors.New("External response is too large")
	}
	return nil
}

func fetchResponse(r *http.Request, requestURL string) (*http.Response, error) {
	response, err := fetchResponseWithClient(r, requestURL, externalHTTPClient)
	if err == nil || r.Context().Err() != nil {
		return response, err
	}
	ipv4Response, ipv4Err := fetchResponseWithClient(r, requestURL, externalIPv4HTTPClient)
	if ipv4Err == nil {
		return ipv4Response, nil
	}
	return nil, errors.Join(err, ipv4Err)
}

func fetchResponseWithClient(r *http.Request, requestURL string, client *http.Client) (*http.Response, error) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "cliff/0.1.0")
	return client.Do(request)
}

func copyBoundedDownload(output io.Writer, input io.Reader, maxBytes int64) error {
	written, err := io.Copy(output, io.LimitReader(input, maxBytes+1))
	if err != nil {
		return err
	}
	if written > maxBytes {
		return errors.New("Download is too large")
	}
	return nil
}

func externalHTTPTransport(forceIPv4 bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = externalResponseHeaderTimeout
	if forceIPv4 {
		transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
			return dialer.DialContext(ctx, "tcp4", address)
		}
	}
	return transport
}
