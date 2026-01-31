package subsonic

import "encoding/xml"

// Response wraps the top-level subsonic-response
type Response struct {
	XMLName                xml.Name                `xml:"http://subsonic.org/restapi subsonic-response" json:"-"`
	Status                 string                  `xml:"status,attr" json:"status"`
	Version                string                  `xml:"version,attr" json:"version"`
	SearchResult3          *SearchResult3          `xml:"searchResult3,omitempty" json:"searchResult3,omitempty"`
	Playlists              *Playlists              `xml:"playlists,omitempty" json:"playlists,omitempty"`
	Playlist               *Playlist               `xml:"playlist,omitempty" json:"playlist,omitempty"`
	Artist                 *ArtistWithAlbums       `xml:"artist,omitempty" json:"artist,omitempty"`
	Album                  *AlbumWithSongs         `xml:"album,omitempty" json:"album,omitempty"`
	Song                   *Song                   `xml:"song,omitempty" json:"song,omitempty"`
	Lyrics                 *Lyrics                 `xml:"lyrics,omitempty" json:"lyrics,omitempty"`
	OpenSubsonicExtensions *OpenSubsonicExtensions `xml:"openSubsonicExtensions,omitempty" json:"openSubsonicExtensions,omitempty"`
	Error                  *Error                  `xml:"error,omitempty" json:"error,omitempty"`
}

type ArtistWithAlbums struct {
	Artist
	Album []Album `xml:"album"`
}

type AlbumWithSongs struct {
	Album
	Song []Song `xml:"song"`
}

type Lyrics struct {
	Value string `xml:",chardata" json:"value"`
}

type Error struct {
	Code    int    `xml:"code,attr" json:"code"`
	Message string `xml:"message,attr" json:"message"`
}

type SearchResult3 struct {
	Artist   []Artist   `xml:"artist,omitempty" json:"artist,omitempty"`
	Album    []Album    `xml:"album,omitempty" json:"album,omitempty"`
	Song     []Song     `xml:"song,omitempty" json:"song,omitempty"`
	Playlist []Playlist `xml:"playlist,omitempty" json:"playlist,omitempty"`
}

type OpenSubsonicExtensions struct {
	Extension []OpenSubsonicExtension `xml:"extension" json:"extension"`
}

type OpenSubsonicExtension struct {
	Name     string   `xml:"name,attr" json:"name"`
	Versions []string `xml:"version" json:"version"`
}

type Playlists struct {
	Playlist []Playlist `xml:"playlist"`
}

type Playlist struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	SongCount int    `xml:"songCount,attr,omitempty" json:"songCount,omitempty"`
	Duration  int    `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	Created   string `xml:"created,attr,omitempty" json:"created,omitempty"`
	Owner     string `xml:"owner,attr,omitempty" json:"owner,omitempty"`
	Public    bool   `xml:"public,attr,omitempty" json:"public,omitempty"`
	CoverArt  string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	Entry     []Song `xml:"entry,omitempty" json:"entry,omitempty"`
}

type Artist struct {
	ID         string `xml:"id,attr" json:"id"`
	Name       string `xml:"name,attr" json:"name"`
	CoverArt   string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	AlbumCount int    `xml:"albumCount,attr,omitempty" json:"albumCount,omitempty"`
}

type Album struct {
	ID        string `xml:"id,attr" json:"id"`
	Title     string `xml:"title,attr" json:"title"`                   // Or "name" depending on endpoint, usually title or name
	Name      string `xml:"name,attr,omitempty" json:"name,omitempty"` // Navidrome uses 'name' for albums in lists often
	Artist    string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	ArtistID  string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	CoverArt  string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	SongCount int    `xml:"songCount,attr,omitempty" json:"songCount,omitempty"`
	Duration  int    `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	Year      int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Starred   string `xml:"starred,attr,omitempty" json:"starred,omitempty"` // ISO 8601 date
}

type Song struct {
	ID          string `xml:"id,attr" json:"id"`
	Parent      string `xml:"parent,attr,omitempty" json:"parent,omitempty"`
	Title       string `xml:"title,attr" json:"title"`
	IsDir       bool   `xml:"isDir,attr" json:"isDir"`
	Album       string `xml:"album,attr,omitempty" json:"album,omitempty"`
	AlbumID     string `xml:"albumId,attr,omitempty" json:"albumId,omitempty"`
	Artist      string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	ArtistID    string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	CoverArt    string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	Duration    int    `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	BitRate     int    `xml:"bitRate,attr,omitempty" json:"bitRate,omitempty"`
	Track       int    `xml:"track,attr,omitempty" json:"track,omitempty"`
	Year        int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre       string `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	Size        int64  `xml:"size,attr,omitempty" json:"size,omitempty"`
	Suffix      string `xml:"suffix,attr,omitempty" json:"suffix,omitempty"`
	ContentType string `xml:"contentType,attr,omitempty" json:"contentType,omitempty"`
	IsVideo     bool   `xml:"isVideo,attr,omitempty" json:"isVideo,omitempty"`
	Path        string `xml:"path,attr,omitempty" json:"path,omitempty"`
	Starred     string `xml:"starred,attr,omitempty" json:"starred,omitempty"` // ISO 8601 date
	BPM         int    `xml:"bpm,attr,omitempty" json:"bpm,omitempty"`
	Comment     string `xml:"comment,attr,omitempty" json:"comment,omitempty"`
	SortName    string `xml:"sortName,attr,omitempty" json:"sortName,omitempty"`
}
