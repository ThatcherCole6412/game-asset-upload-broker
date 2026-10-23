package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"
)

const uploadExpirySeconds = 600

var safeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type UploadSigner interface {
	PresignPut(context.Context, string, string, PresignPutInput) (PresignedPut, error)
}

type PresignPutInput struct {
	ExpiresSeconds int
	ContentType    string
	MaxBytes       int64
	IdempotencyKey string
}

type PresignedPut struct {
	URL string `json:"url"`
}

type AssetUploadRequest struct {
	AssetID     string `json:"asset_id"`
	PlayerID    string `json:"player_id"`
	LiveEvent   string `json:"live_event"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
}

type AssetUploadGrant struct {
	AssetID        string            `json:"asset_id"`
	ObjectKey      string            `json:"object_key"`
	Moderation     string            `json:"moderation"`
	UploadURL      string            `json:"upload_url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	ExpiresSeconds int               `json:"expires_seconds"`
}

type requestError struct {
	Status  int
	Message string
}

func (e *requestError) Error() string { return e.Message }

type uploadBroker struct {
	bucket string
	signer UploadSigner
}

func (b uploadBroker) Grant(ctx context.Context, in AssetUploadRequest) (AssetUploadGrant, error) {
	if !safeID.MatchString(in.AssetID) || !safeID.MatchString(in.PlayerID) || !safeID.MatchString(in.LiveEvent) {
		return AssetUploadGrant{}, &requestError{Status: http.StatusBadRequest, Message: "asset_id, player_id, and live_event must use letters, numbers, underscore, or dash"}
	}
	allowed := map[string]int64{
		"image/png":  8 << 20,
		"image/jpeg": 8 << 20,
		"audio/ogg":  20 << 20,
	}
	limit, ok := allowed[in.ContentType]
	if !ok {
		return AssetUploadGrant{}, &requestError{Status: http.StatusUnsupportedMediaType, Message: "content_type is not accepted for player assets"}
	}
	if in.Bytes <= 0 || in.Bytes > limit {
		return AssetUploadGrant{}, &requestError{Status: http.StatusUnprocessableEntity, Message: fmt.Sprintf("bytes must be between 1 and %d for %s", limit, in.ContentType)}
	}
	ext := strings.ToLower(path.Ext(in.Filename))
	if ext == "" || strings.ContainsAny(ext, `/\\`) {
		return AssetUploadGrant{}, &requestError{Status: http.StatusBadRequest, Message: "filename must include an extension"}
	}
	objectKey := path.Join("moderation", "pending", in.LiveEvent, in.PlayerID, in.AssetID+ext)
	signed, err := b.signer.PresignPut(ctx, b.bucket, objectKey, PresignPutInput{
		ExpiresSeconds: uploadExpirySeconds,
		ContentType:    in.ContentType,
		MaxBytes:       in.Bytes,
		IdempotencyKey: in.AssetID,
	})
	if err != nil {
		return AssetUploadGrant{}, err
	}
	return AssetUploadGrant{
		AssetID: in.AssetID, ObjectKey: objectKey, Moderation: "pending", UploadURL: signed.URL,
		Method: http.MethodPut, Headers: map[string]string{"Content-Type": in.ContentType}, ExpiresSeconds: uploadExpirySeconds,
	}, nil
}

func (b uploadBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/asset-uploads" {
		http.NotFound(w, r)
		return
	}
	defer r.Body.Close()
	var in AssetUploadRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		return
	}
	grant, err := b.Grant(r.Context(), in)
	if err != nil {
		var inputErr *requestError
		if errors.As(err, &inputErr) {
			writeJSON(w, inputErr.Status, map[string]string{"error": inputErr.Message})
			return
		}
		var apiErr *InfraiError
		if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 {
			writeJSON(w, apiErr.Status, map[string]string{"error": apiErr.Message})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not issue upload grant"})
		return
	}
	writeJSON(w, http.StatusCreated, grant)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func stableBucketName(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return "game-assets-" + hex.EncodeToString(sum[:6])
}
