package main

import (
	"context"
	"errors"
	"testing"
)

type recordingSigner struct {
	calls  int
	bucket string
	key    string
	input  PresignPutInput
}

func (s *recordingSigner) PresignPut(_ context.Context, bucket, key string, input PresignPutInput) (PresignedPut, error) {
	s.calls++
	s.bucket, s.key, s.input = bucket, key, input
	return PresignedPut{URL: "https://uploads.example/signed"}, nil
}

func TestGrantModerationDecision(t *testing.T) {
	tests := []struct {
		name       string
		input      AssetUploadRequest
		wantStatus string
		wantKey    string
		wantCalls  int
		wantCode   int
	}{
		{
			name:       "accepted event artwork enters pending moderation",
			input:      AssetUploadRequest{AssetID: "asset-42", PlayerID: "player-7", LiveEvent: "winter-cup", Filename: "banner.PNG", ContentType: "image/png", Bytes: 4096},
			wantStatus: "pending", wantKey: "moderation/pending/winter-cup/player-7/asset-42.png", wantCalls: 1,
		},
		{
			name:      "oversized artwork is rejected before signing",
			input:     AssetUploadRequest{AssetID: "asset-43", PlayerID: "player-7", LiveEvent: "winter-cup", Filename: "banner.png", ContentType: "image/png", Bytes: (8 << 20) + 1},
			wantCalls: 0, wantCode: 422,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer := &recordingSigner{}
			grant, err := (uploadBroker{bucket: "game-assets", signer: signer}).Grant(context.Background(), tt.input)
			if tt.wantCode != 0 {
				var reqErr *requestError
				if !errors.As(err, &reqErr) || reqErr.Status != tt.wantCode {
					t.Fatalf("error = %v, want request status %d", err, tt.wantCode)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if signer.calls != tt.wantCalls {
				t.Fatalf("signer calls = %d, want %d", signer.calls, tt.wantCalls)
			}
			if tt.wantCalls == 1 {
				if grant.Moderation != tt.wantStatus || grant.ObjectKey != tt.wantKey {
					t.Fatalf("grant = %#v", grant)
				}
				if signer.input.IdempotencyKey != tt.input.AssetID || signer.input.ContentType != tt.input.ContentType || signer.input.MaxBytes != tt.input.Bytes {
					t.Fatalf("presign boundary = %#v", signer.input)
				}
			}
		})
	}
}
