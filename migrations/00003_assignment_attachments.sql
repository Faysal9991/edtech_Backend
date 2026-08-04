-- +goose Up
CREATE TABLE assignment_assets (
    assignment_id uuid NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    media_asset_id uuid NOT NULL REFERENCES media_assets(id),
    PRIMARY KEY (assignment_id, media_asset_id)
);
CREATE INDEX assignment_assets_media_idx ON assignment_assets (media_asset_id, assignment_id);

-- +goose Down
DROP TABLE IF EXISTS assignment_assets;
