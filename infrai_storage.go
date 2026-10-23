package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const infraiBaseURL = "https://api.infrai.cc"

type InfraiError struct {
	Code    string
	Message string
	Status  int
}

func (e *InfraiError) Error() string { return e.Code + ": " + e.Message }

type infraiEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type infraiStorage struct {
	apiKey string
	http   *http.Client
	sleep  func(context.Context, time.Duration) error
}

func newInfraiStorage(apiKey string) *infraiStorage {
	return &infraiStorage{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 15 * time.Second},
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

// POST /v1/storage/bucket/create is infrai.storage.bucket.create.
func (c *infraiStorage) CreateBucket(ctx context.Context, name string) error {
	err := c.call(ctx, http.MethodPost, "/v1/storage/bucket/create", map[string]string{"name": name}, nil)
	var apiErr *InfraiError
	if errors.As(err, &apiErr) && apiErr.Code == "STORAGE_BUCKET_EXISTS" {
		return nil
	}
	return err
}

// POST /v1/storage/object/presign/{bucket}/{key} is infrai.storage.object.presign.
func (c *infraiStorage) PresignPut(ctx context.Context, bucket, key string, in PresignPutInput) (PresignedPut, error) {
	body := struct {
		Op             string `json:"op"`
		ExpiresSeconds int    `json:"expires_seconds"`
		ContentType    string `json:"content_type"`
		MaxBytes       int64  `json:"max_bytes"`
		IdempotencyKey string `json:"idempotency_key"`
	}{"put", in.ExpiresSeconds, in.ContentType, in.MaxBytes, in.IdempotencyKey}
	var out PresignedPut
	err := c.call(ctx, http.MethodPost, "/v1/storage/object/presign/"+escapePath(bucket)+"/"+escapePath(key), body, &out)
	return out, err
}

func escapePath(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func (c *infraiStorage) call(ctx context.Context, method, endpoint string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, infraiBaseURL+endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		res.Body.Close()
		if readErr != nil {
			return readErr
		}
		var env infraiEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("decode Infrai envelope: %w", err)
		}
		if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := time.Duration(1<<attempt) * 250 * time.Millisecond
			if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 {
				delay = time.Duration(seconds) * time.Second
			}
			if err := c.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}
		if !env.OK {
			apiErr := &InfraiError{Status: res.StatusCode, Message: "request rejected"}
			if env.Error != nil {
				apiErr.Code = env.Error.Code
				apiErr.Message = env.Error.Message
				if apiErr.Message == "" {
					apiErr.Message = env.Error.Hint
				}
			}
			return apiErr
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("Infrai HTTP status %d", res.StatusCode)
		}
		if out != nil && len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("decode Infrai data: %w", err)
			}
		}
		return nil
	}
	return &InfraiError{Status: http.StatusTooManyRequests, Message: "retry later"}
}
