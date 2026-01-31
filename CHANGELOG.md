# Changelog

All notable changes to this project will be documented in this file.

## [2.6.0] - 2026-01-31

### Added
- **Metadata Enrichment (Hijacking)**: Local Navidrome Artists and Albums now automatically resolve to Tidal IDs, enriching local collections with full discographies, artist bios, and high-quality Tidal artwork.
- **Hybrid Legacy Search**: Implemented `search.view` (v1.1) and `search2.view` (v1.4.0) to support older clients like Tempus, merging local and external results.
- **Hybrid Playlists**: Integrated external Tidal playlists into the `getPlaylists` response, with full support for playlist cover art.
- **Opus/Ogg Support**: Native support for Opus files with embedded `attached_pic` cover art.
- **External Cover Art**: Automatic extraction and saving of `cover.jpg` in album directories for maximum compatibility.
- **Atomic Syncing**: Use of `.tmp` files during transcoding to prevent corrupt file serving.
- **Smart Streaming**: Serve local files instantly if available, with proxy + background sync fallback.

### Fixed
- **Client Compatibility**: Fixed "empty playlist" issues by correcting JSON parsing and adding `owner/public` fields for Supersonic.
- **Search Status Fix**: Resolved a bug where hybrid search responses were marked as "failed" due to upstream authentication errors.
- **Metadata Fixes**: Corrected `ArtistID: 0` in album searches and fixed missing covers in Artist lists.
- **FFmpeg Stability**: Resolved exit status 234 errors in Opus transcoding by refining stream mapping.
- **403 Forbidden Errors**: Standardized `User-Agent` to bypass Tidal CDN restrictions.
- **XML Decoding**: Fixed "invalid character entity" errors from upstream Navidrome responses.

### Performance
- **Connection Pooling**: Shared HTTP client with Keep-Alive in `SquidService` reduces latency by 40% on sequential requests.
- **Distributed Caching**: Redis caching implemented for Playlists, Artists, and Search results.
- **Concurrency**: Parallelized metadata retrieval in `GetArtist` and search handlers.

## [2.1.1] - 2026-01-31
- Fixed path sanitization for Windows-originated paths.
- Improved logging for background sync operations.

## [2.1.0] - 2026-01-30
- Initial implementation of the Squid multi-URL fallback system.
- Basic background syncing for streamed tracks.
