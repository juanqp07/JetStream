# Changelog

All notable changes to this project will be documented in this file.

## [2.0.0] - 2026-01-31

### Added
- **Opus/Ogg Support**: Implemented native support for Opus files.
- **External Cover Art**: Automatic extraction and saving of `cover.jpg` in album directories to ensure cover art visibility in Navidrome for Opus files.
- **Smart Streaming**: Optimized `Stream` handler to serve local files instantly if available, while maintaining proxy + background sync fallback for new tracks.
- **Hybrid Playlists**: Integrated external Tidal playlists into the `getPlaylists` response, merging them with local Navidrome playlists.
- **Atomic Operations**: Implemented `.tmp` file usage during transcoding to prevent corrupt file serving and ensure sync atomicity.

### Fixed
- **403 Forbidden Errors**: Resolved Tidal CDN access issues by standardizing `User-Agent` headers across all services (Proxy, Sync, Metadata).
- **XML Decoding**: Fixed "invalid character entity" errors when parsing upstream Navidrome responses by handling compression correctly.
- **FFmpeg Stability**: Resolved exit status 234 errors in Opus transcoding by refining stream mapping and forcing output formats for temporary files.
- **Path Sanitization**: Standardized path cleaning across all components to ensure local file matching is consistent and robust.
- **Playlist Compatibility**: Fixed "empty playlist" issues by correcting JSON parsing for the Squid API (root-level fields) and adding `owner/public` fields for Supersonic compatibility.
- **Duplicate Albums**: Implemented deduplication logic in `GetArtist` to prevent multiple versions of the same album from clogging the view.
- **Missing Covers**: Fixed missing cover art in Artist/Album lists by checking and propagating playlist/album cover IDs correctly.

### Performance
- **Connection Pooling**: Implemented a shared HTTP client with Keep-Alive in `SquidService` to reduce SSL handshake latency.
- **Caching**: Added Redis caching for `GetPlaylist` and optimized `GetArtist` efficiency.
- **Parallelization**: Refactored `GetArtist` to fetch metadata and albums concurrently.

### Changed
- Refactored `SyncService` for better dependency injection and background task management.
- Updated Subsonic models to support `CoverArt` and `squareImage` in playlists.
- Improved logging for background sync and FFmpeg operations.
