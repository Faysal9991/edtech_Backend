package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/Faysal9991/edtech_Backend/internal/platform/clock"
	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/Faysal9991/edtech_Backend/internal/platform/database"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	"github.com/Faysal9991/edtech_Backend/internal/platform/queue"
	"github.com/Faysal9991/edtech_Backend/internal/platform/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrNotFound      = errors.New("media not found")
	ErrForbidden     = errors.New("media access denied")
	ErrInvalidObject = errors.New("uploaded object does not match intent")
)

type Service struct {
	db    database.Beginner
	q     *data.Queries
	ids   platformid.Generator
	clock clock.Clock
	store storage.Store
	queue queue.Client
	cfg   config.Config
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, c clock.Clock, store storage.Store, jobs queue.Client, cfg config.Config) *Service {
	return &Service{db: db, q: q, ids: ids, clock: c, store: store, queue: jobs, cfg: cfg}
}

var allowed = map[string]map[string][]string{"video": {"video/mp4": {".mp4"}}, "pdf": {"application/pdf": {".pdf"}}, "image": {"image/jpeg": {".jpg", ".jpeg"}, "image/png": {".png"}, "image/webp": {".webp"}}, "assignment": {"application/pdf": {".pdf"}, "image/jpeg": {".jpg", ".jpeg"}, "image/png": {".png"}, "application/zip": {".zip"}, "text/plain": {".txt"}}}

func (s *Service) validate(in api.UploadIntentWrite) error {
	kind := string(in.Kind)
	types, ok := allowed[kind]
	if !ok {
		return errors.New("unsupported media kind")
	}
	ext := strings.ToLower(filepath.Ext(in.Filename))
	if filepath.Base(in.Filename) != in.Filename || strings.Contains(in.Filename, "..") || ext == "" {
		return errors.New("filename must be a safe base name with an extension")
	}
	extensions, ok := types[strings.ToLower(in.ContentType)]
	if !ok {
		return errors.New("content type is not allowed for media kind")
	}
	validExt := false
	for _, v := range extensions {
		if ext == v {
			validExt = true
		}
	}
	if !validExt {
		return errors.New("file extension does not match content type")
	}
	limit := s.cfg.Upload.MaxPDFBytes
	switch kind {
	case "video":
		limit = s.cfg.Upload.MaxVideoBytes
	case "image":
		limit = s.cfg.Upload.MaxImageBytes
	}
	if in.SizeBytes < 1 || in.SizeBytes > limit {
		return fmt.Errorf("file size exceeds %d byte limit", limit)
	}
	return nil
}

func optionalText(v *string) pgtype.Text {
	if v == nil || *v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
func (s *Service) CreateIntent(ctx context.Context, orgID, userID uuid.UUID, in api.UploadIntentWrite) (api.UploadIntent, error) {
	if err := s.validate(in); err != nil {
		return api.UploadIntent{}, err
	}
	assetID, intentID := s.ids.New(), s.ids.New()
	ext := strings.ToLower(filepath.Ext(in.Filename))
	key := fmt.Sprintf("organizations/%s/users/%s/%s%s", orgID, userID, assetID, ext)
	expires := s.clock.Now().Add(s.cfg.Storage.SignedURLTTL)
	var intent data.UploadIntent
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		_, err := q.CreateMediaAsset(ctx, data.CreateMediaAssetParams{ID: assetID, OrganizationID: orgID, OwnerUserID: userID, Kind: string(in.Kind), StorageKey: key, OriginalFilename: in.Filename, ContentType: strings.ToLower(in.ContentType), SizeBytes: in.SizeBytes, ChecksumSha256: optionalText(in.ChecksumSha256)})
		if err != nil {
			return err
		}
		intent, err = q.CreateUploadIntent(ctx, data.CreateUploadIntentParams{ID: intentID, MediaAssetID: assetID, OwnerUserID: userID, ExpectedSizeBytes: in.SizeBytes, ExpectedContentType: strings.ToLower(in.ContentType), ExpectedChecksumSha256: optionalText(in.ChecksumSha256), ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
		return err
	})
	if err != nil {
		return api.UploadIntent{}, err
	}
	url, err := s.store.PresignUpload(ctx, key, in.ContentType, s.cfg.Storage.SignedURLTTL)
	if err != nil {
		return api.UploadIntent{}, err
	}
	return api.UploadIntent{Id: intent.ID, MediaAssetId: assetID, UploadUrl: url, ExpiresAt: expires}, nil
}

func (s *Service) Complete(ctx context.Context, intentID, userID uuid.UUID) (data.MediaAsset, error) {
	joined, err := s.q.GetUploadIntentWithAsset(ctx, intentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.MediaAsset{}, ErrNotFound
	}
	if err != nil {
		return data.MediaAsset{}, err
	}
	if joined.OwnerUserID != userID {
		return data.MediaAsset{}, ErrForbidden
	}
	if joined.Status == "completed" {
		return s.q.GetMediaAsset(ctx, joined.MediaAssetID)
	}
	if !joined.ExpiresAt.Valid || !joined.ExpiresAt.Time.After(s.clock.Now()) {
		return data.MediaAsset{}, errors.New("upload intent expired")
	}
	info, err := s.store.Head(ctx, joined.StorageKey)
	if err != nil {
		return data.MediaAsset{}, ErrInvalidObject
	}
	if info.Size != joined.ExpectedSizeBytes || !strings.EqualFold(info.ContentType, joined.ExpectedContentType) {
		return data.MediaAsset{}, ErrInvalidObject
	}
	var asset data.MediaAsset
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.GetUploadIntentForUpdate(ctx, intentID)
		if err != nil {
			return err
		}
		if locked.Status == "completed" {
			asset, err = q.GetMediaAsset(ctx, locked.MediaAssetID)
			return err
		}
		if _, err = q.CompleteUploadIntent(ctx, intentID); err != nil {
			return err
		}
		asset, err = q.SetMediaAssetStatus(ctx, data.SetMediaAssetStatusParams{ID: joined.MediaAssetID, Status: "uploaded", FailureReason: pgtype.Text{}})
		return err
	})
	if err != nil {
		return data.MediaAsset{}, err
	}
	if err := s.queue.Enqueue(queue.TypeMediaProcess, map[string]string{"media_asset_id": asset.ID.String()}); err != nil {
		return data.MediaAsset{}, err
	}
	return asset, nil
}

func (s *Service) AccessURL(ctx context.Context, assetID, userID uuid.UUID, privileged bool) (string, error) {
	asset, err := s.q.GetMediaAsset(ctx, assetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if asset.Status != "ready" {
		return "", errors.New("media is not ready")
	}
	if !privileged {
		ok, err := s.q.CanUserAccessMedia(ctx, data.CanUserAccessMediaParams{MediaAssetID: assetID, UserID: userID})
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrForbidden
		}
	}
	return s.store.PresignDownload(ctx, asset.StorageKey, s.cfg.Storage.SignedURLTTL)
}

type probeResult struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int32  `json:"width"`
		Height    int32  `json:"height"`
	} `json:"streams"`
}

func (s *Service) Process(ctx context.Context, assetID uuid.UUID) error {
	asset, err := s.q.GetMediaAsset(ctx, assetID)
	if err != nil {
		return err
	}
	if asset.Status == "ready" {
		return nil
	}
	if asset.Status != "uploaded" && asset.Status != "processing" {
		return fmt.Errorf("asset status %s is not processable", asset.Status)
	}
	if _, err = s.q.SetMediaAssetStatus(ctx, data.SetMediaAssetStatusParams{ID: assetID, Status: "processing", FailureReason: pgtype.Text{}}); err != nil {
		return err
	}
	failure := func(processErr error) error {
		_, _ = s.q.SetMediaAssetStatus(ctx, data.SetMediaAssetStatusParams{ID: assetID, Status: "failed", FailureReason: pgtype.Text{String: truncate(processErr.Error(), 500), Valid: true}})
		return processErr
	}
	reader, err := s.store.Get(ctx, asset.StorageKey)
	if err != nil {
		return failure(err)
	}
	defer reader.Close()
	temp, err := os.CreateTemp("", "lms-media-*")
	if err != nil {
		return failure(err)
	}
	path := temp.Name()
	defer os.Remove(path)
	hasher := sha256.New()
	copied, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(reader, asset.SizeBytes+1))
	closeErr := temp.Close()
	if err != nil {
		return failure(err)
	}
	if closeErr != nil {
		return failure(closeErr)
	}
	if copied != asset.SizeBytes {
		return failure(errors.New("downloaded media size does not match verified size"))
	}
	if asset.ChecksumSha256.Valid && !strings.EqualFold(asset.ChecksumSha256.String, fmt.Sprintf("%x", hasher.Sum(nil))) {
		return failure(errors.New("downloaded media SHA-256 checksum does not match upload intent"))
	}
	duration, width, height := pgtype.Int4{}, pgtype.Int4{}, pgtype.Int4{}
	metadata := map[string]any{}
	switch asset.Kind {
	case "video":
		command := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_type,width,height", "-of", "json", path)
		output, err := command.Output()
		if err != nil {
			return failure(fmt.Errorf("ffprobe media: %w", err))
		}
		var probe probeResult
		if err := json.Unmarshal(output, &probe); err != nil {
			return failure(err)
		}
		var seconds float64
		if _, err := fmt.Sscanf(probe.Format.Duration, "%f", &seconds); err == nil && seconds >= 0 {
			duration = pgtype.Int4{Int32: int32(seconds), Valid: true}
		}
		for _, stream := range probe.Streams {
			if stream.CodecType == "video" {
				width = pgtype.Int4{Int32: stream.Width, Valid: stream.Width > 0}
				height = pgtype.Int4{Int32: stream.Height, Valid: stream.Height > 0}
				break
			}
		}
		thumbPath := path + ".jpg"
		defer os.Remove(thumbPath)
		if err := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "00:00:01", "-i", path, "-frames:v", "1", "-vf", "scale=640:-2", thumbPath).Run(); err != nil {
			return failure(fmt.Errorf("create video thumbnail: %w", err))
		}
		thumb, err := os.Open(thumbPath)
		if err != nil {
			return failure(err)
		}
		stat, err := thumb.Stat()
		if err != nil {
			thumb.Close()
			return failure(err)
		}
		thumbID := s.ids.New()
		thumbKey := fmt.Sprintf("organizations/%s/media-thumbnails/%s.jpg", asset.OrganizationID, thumbID)
		if _, err := s.store.Put(ctx, thumbKey, "image/jpeg", thumb, stat.Size()); err != nil {
			thumb.Close()
			return failure(err)
		}
		thumb.Close()
		if _, err := s.q.CreateMediaAsset(ctx, data.CreateMediaAssetParams{ID: thumbID, OrganizationID: asset.OrganizationID, OwnerUserID: asset.OwnerUserID, Kind: "image", StorageKey: thumbKey, OriginalFilename: asset.ID.String() + "-thumbnail.jpg", ContentType: "image/jpeg", SizeBytes: stat.Size(), ChecksumSha256: pgtype.Text{}}); err != nil {
			return failure(err)
		}
		if _, err := s.q.SetMediaAssetProcessed(ctx, data.SetMediaAssetProcessedParams{ID: thumbID, DurationSeconds: pgtype.Int4{}, Width: pgtype.Int4{}, Height: pgtype.Int4{}, Metadata: []byte(`{}`)}); err != nil {
			return failure(err)
		}
		metadata["thumbnail_asset_id"] = thumbID.String()
	case "pdf":
		file, err := os.Open(path)
		if err != nil {
			return failure(err)
		}
		header := make([]byte, 5)
		_, err = io.ReadFull(file, header)
		file.Close()
		if err != nil || string(header) != "%PDF-" {
			return failure(errors.New("invalid PDF signature"))
		}
	case "image":
		file, err := os.Open(path)
		if err != nil {
			return failure(err)
		}
		cfg, _, err := image.DecodeConfig(file)
		file.Close()
		if err != nil {
			return failure(fmt.Errorf("decode image: %w", err))
		}
		width = pgtype.Int4{Int32: int32(cfg.Width), Valid: true}
		height = pgtype.Int4{Int32: int32(cfg.Height), Valid: true}
	case "assignment":
		file, err := os.Open(path)
		if err != nil {
			return failure(err)
		}
		header := make([]byte, 8192)
		read, readErr := file.Read(header)
		file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return failure(readErr)
		}
		header = header[:read]
		switch asset.ContentType {
		case "application/pdf":
			if len(header) < 5 || string(header[:5]) != "%PDF-" {
				return failure(errors.New("invalid PDF signature"))
			}
		case "image/jpeg", "image/png":
			file, err := os.Open(path)
			if err != nil {
				return failure(err)
			}
			cfg, _, err := image.DecodeConfig(file)
			file.Close()
			if err != nil {
				return failure(fmt.Errorf("decode assignment image: %w", err))
			}
			width = pgtype.Int4{Int32: int32(cfg.Width), Valid: true}
			height = pgtype.Int4{Int32: int32(cfg.Height), Valid: true}
		case "application/zip":
			if len(header) < 4 || !(bytes.Equal(header[:4], []byte{'P', 'K', 3, 4}) || bytes.Equal(header[:4], []byte{'P', 'K', 5, 6}) || bytes.Equal(header[:4], []byte{'P', 'K', 7, 8})) {
				return failure(errors.New("invalid ZIP signature"))
			}
		case "text/plain":
			if bytes.IndexByte(header, 0) >= 0 {
				return failure(errors.New("plain-text assignment contains binary data"))
			}
		default:
			return failure(errors.New("unsupported assignment content type"))
		}
	}
	payload, _ := json.Marshal(metadata)
	_, err = s.q.SetMediaAssetProcessed(ctx, data.SetMediaAssetProcessedParams{ID: asset.ID, DurationSeconds: duration, Width: width, Height: height, Metadata: payload})
	if err != nil {
		return failure(err)
	}
	return nil
}
func truncate(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}
