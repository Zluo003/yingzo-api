-- Keep the electron-updater ZIP and the user-facing macOS DMG on one release.
-- Windows uses the same executable for both update and first-time install.
ALTER TABLE yingzo_desktop_releases
    ADD COLUMN IF NOT EXISTS installer_filename TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS installer_storage_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS installer_size_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS installer_sha256 CHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS installer_sha512 TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS installer_download_url TEXT NOT NULL DEFAULT '';

UPDATE yingzo_desktop_releases
SET installer_filename = package_filename,
    installer_storage_key = storage_key,
    installer_size_bytes = package_size_bytes,
    installer_sha256 = sha256,
    installer_sha512 = sha512,
    installer_download_url = download_url
WHERE installer_storage_key = '';
