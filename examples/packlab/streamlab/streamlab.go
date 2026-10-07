// Package streamlab is StreamLab, a video service that delivers HLS: a
// catalogue, playback sessions with signed tokens, multivariant and media
// playlists, MPEG-TS segments in four renditions for 30 on-demand videos,
// two live channels whose playlists slide forward every two seconds, and
// playback heartbeats. It is the reference app for the streaming pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - package: every segment request packages the segment again (muxing
//     MPEG-TS packets), so a live event's viewers, who all want the same
//     newest segment, repeat the same work thousands of times (fix
//     "package" keeps packaged segments in a bounded cache).
//   - playlist: every live playlist request lists every segment since the
//     event started to find the newest six, so playlists slow down as the
//     event goes on (fix "playlist" renders each playlist once per segment
//     and serves that).
//   - startup: starting playback checks entitlement, fetches a DRM license
//     and picks a CDN one after another (fix "startup" does the three at
//     once).
package streamlab

import (
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Videos is how many on-demand videos StreamLab has (v001 ... v030).
	Videos = 30
	// Segments is how many segments each video has.
	Segments = 300
	window   = 6 // live playlist length in segments
	tsPacket = 188
)

type rendition struct {
	Name       string
	Bandwidth  int // bits per second
	Resolution string
}

var renditions = []rendition{
	{"240p", 400_000, "426x240"},
	{"480p", 1_200_000, "854x480"},
	{"720p", 3_000_000, "1280x720"},
	{"1080p", 6_000_000, "1920x1080"},
}

var channels = []string{"live1", "live2"}

type session struct {
	ID       string
	Resource string // "vod/v001" or "live/live1"
	Started  time.Time
	beats    int
}

type server struct {
	cfg     labkit.Config
	segDur  time.Duration
	scale   int // segment bytes are divided by this (fast mode)
	backend time.Duration
	secret  []byte
	start   time.Time // when the live channels went on air

	smu      sync.Mutex
	sessions map[string]*session

	// The packaged-segment cache (package fix).
	cmu   sync.Mutex
	lru   *list.List
	items map[string]*list.Element
	bytes int
	max   int

	// Rendered live playlists, one per channel and rendition (playlist fix).
	pmu       sync.Mutex
	playlists map[string]cachedPlaylist

	packaged labkit.Counter // segments packaged
	listed   labkit.Counter // live segments listed to build playlists
	backends labkit.Gauge   // startup backend calls in flight
}

type cachedPlaylist struct {
	seq  int
	body string
}

type lruItem struct {
	key string
	b   []byte
}

// New returns StreamLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, segDur: 2 * time.Second, scale: 1, backend: 40 * time.Millisecond,
		secret: []byte("streamlab-signing-key"), start: time.Now(), sessions: map[string]*session{},
		lru: list.New(), items: map[string]*list.Element{}, max: 128 << 20, playlists: map[string]cachedPlaylist{}}
	if cfg.Fast {
		s.segDur, s.scale, s.backend = 200*time.Millisecond, 100, time.Millisecond
	}
	// The live channels have been on air for an hour.
	s.start = s.start.Add(-time.Hour)
	routes := []labkit.Route{
		{Method: "GET", Path: "/api/videos", Tag: "videos", Summary: "The video catalogue"},
		{Method: "GET", Path: "/api/videos/{id}", Tag: "videos", Summary: "One video"},
		{Method: "GET", Path: "/api/live", Tag: "live", Summary: "Live channels and their viewers"},
		{Method: "POST", Path: "/api/playback", Tag: "playback", Summary: "Start playback of a video (videoId) or a live channel (channel); returns the manifest URL"},
		{Method: "POST", Path: "/api/playback/{id}/heartbeat", Tag: "playback", Summary: "Player heartbeat: position, rendition, buffer, stalls"},
		{Method: "GET", Path: "/vod/{id}/master.m3u8", Tag: "hls", Summary: "Multivariant playlist (token)"},
		{Method: "GET", Path: "/vod/{id}/{rendition}/index.m3u8", Tag: "hls", Summary: "Media playlist (token)"},
		{Method: "GET", Path: "/vod/{id}/{rendition}/{segment}", Tag: "hls", Summary: "MPEG-TS segment (token)"},
		{Method: "GET", Path: "/live/{channel}/master.m3u8", Tag: "hls", Summary: "Live multivariant playlist (token)"},
		{Method: "GET", Path: "/live/{channel}/{rendition}/index.m3u8", Tag: "hls", Summary: "Live media playlist, a sliding window (token)"},
		{Method: "GET", Path: "/live/{channel}/{rendition}/{segment}", Tag: "hls", Summary: "Live MPEG-TS segment (token)"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "StreamLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("StreamLab", "Video on demand and live streaming over HLS, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/videos", s.listVideos)
	mux.HandleFunc("GET /api/videos/{id}", s.getVideo)
	mux.HandleFunc("GET /api/live", s.liveStatus)
	mux.HandleFunc("POST /api/playback", s.playback)
	mux.HandleFunc("POST /api/playback/{id}/heartbeat", s.heartbeat)
	mux.HandleFunc("GET /vod/{id}/master.m3u8", s.vodMaster)
	mux.HandleFunc("GET /vod/{id}/{rendition}/index.m3u8", s.vodMedia)
	mux.HandleFunc("GET /vod/{id}/{rendition}/{segment}", s.vodSegment)
	mux.HandleFunc("GET /live/{channel}/master.m3u8", s.liveMaster)
	mux.HandleFunc("GET /live/{channel}/{rendition}/index.m3u8", s.liveMedia)
	mux.HandleFunc("GET /live/{channel}/{rendition}/{segment}", s.liveSegment)
	return s, mux
}

var titles = []string{"Ocean Deep", "City Lights", "Mountain Trail", "Night Market", "Desert Run", "River Song",
	"Winter Games", "Forest Floor", "Street Food", "Northern Sky"}

func videoID(n int) string { return fmt.Sprintf("v%03d", n) }

func validVideo(id string) bool {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "v"))
	return strings.HasPrefix(id, "v") && err == nil && n >= 1 && n <= Videos && id == videoID(n)
}

func validChannel(c string) bool {
	for _, x := range channels {
		if x == c {
			return true
		}
	}
	return false
}

func findRendition(name string) *rendition {
	for i := range renditions {
		if renditions[i].Name == name {
			return &renditions[i]
		}
	}
	return nil
}

func (s *server) video(n int) map[string]any {
	id := videoID(n)
	return map[string]any{"id": id, "title": fmt.Sprintf("%s %d", titles[(n-1)%len(titles)], (n-1)/len(titles)+1),
		"durationSeconds": int(time.Duration(Segments) * 2 * time.Second / time.Second), "renditions": []string{"240p", "480p", "720p", "1080p"}}
}

func (s *server) listVideos(w http.ResponseWriter, _ *http.Request) {
	out := make([]map[string]any, 0, Videos)
	for n := 1; n <= Videos; n++ {
		out = append(out, s.video(n))
	}
	labkit.JSON(w, 200, map[string]any{"videos": out})
}

func (s *server) getVideo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validVideo(id) {
		labkit.Error(w, 404, "not_found", "no such video")
		return
	}
	n, _ := strconv.Atoi(id[1:])
	labkit.JSON(w, 200, s.video(n))
}

// live returns the sequence number of the newest complete live segment.
func (s *server) live() int {
	return int(time.Since(s.start) / s.segDur)
}

func (s *server) liveStatus(w http.ResponseWriter, _ *http.Request) {
	s.smu.Lock()
	viewers := map[string]int{}
	for _, ss := range s.sessions {
		if strings.HasPrefix(ss.Resource, "live/") {
			viewers[strings.TrimPrefix(ss.Resource, "live/")]++
		}
	}
	s.smu.Unlock()
	var out []map[string]any
	for _, c := range channels {
		out = append(out, map[string]any{"channel": c, "live": true, "sequence": s.live(), "sessions": viewers[c]})
	}
	labkit.JSON(w, 200, map[string]any{"channels": out})
}

// --- playback sessions and tokens ---------------------------------------

func (s *server) sign(resource string, exp int64) string {
	m := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(m, "%s|%d", resource, exp)
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(m.Sum(nil))[:32]
}

// verify checks a playback token for a resource ("vod/v001", "live/live1")
// and returns it as the server signs it, for the URIs in playlists.
func (s *server) verify(w http.ResponseWriter, r *http.Request, resource string) (string, bool) {
	tok := r.URL.Query().Get("token")
	exp, _, ok := strings.Cut(tok, ".")
	e, err := strconv.ParseInt(exp, 10, 64)
	signed := s.sign(resource, e)
	if !ok || err != nil || time.Now().Unix() > e || !hmac.Equal([]byte(tok), []byte(signed)) {
		labkit.Error(w, 403, "bad_token", "missing, expired or wrong playback token")
		return "", false
	}
	return signed, true
}

// startupChecks are the calls playback makes before it can start.
func (s *server) startupChecks() {
	call := func() {
		done := s.backends.Enter()
		time.Sleep(s.backend)
		done()
	}
	if !s.cfg.Fixes.On("startup") {
		// Bottleneck: entitlement, DRM license, CDN choice, in turn.
		call()
		call()
		call()
		return
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); call() }()
	}
	wg.Wait()
}

func (s *server) playback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		VideoID string `json:"videoId"`
		Channel string `json:"channel"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	var resource string
	switch {
	case in.VideoID != "" && validVideo(in.VideoID):
		resource = "vod/" + in.VideoID
	case in.Channel != "" && validChannel(in.Channel):
		resource = "live/" + in.Channel
	default:
		labkit.Error(w, 404, "not_found", "give a videoId (v001-v030) or a live channel")
		return
	}
	s.startupChecks()
	exp := time.Now().Add(4 * time.Hour).Unix()
	tok := s.sign(resource, exp)
	ss := &session{ID: labkit.Token("ps_")[:19], Resource: resource, Started: time.Now()}
	s.smu.Lock()
	s.sessions[ss.ID] = ss
	s.smu.Unlock()
	labkit.JSON(w, 201, map[string]any{"sessionId": ss.ID, "token": tok, "manifest": "/" + resource + "/master.m3u8?token=" + tok,
		"heartbeat": "/api/playback/" + ss.ID + "/heartbeat", "heartbeatSeconds": 30})
}

func (s *server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Position  float64 `json:"position"`
		Rendition string  `json:"rendition"`
		Buffer    float64 `json:"bufferSeconds"`
		Stalls    int     `json:"stalls"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	s.smu.Lock()
	ss := s.sessions[r.PathValue("id")]
	if ss != nil {
		ss.beats++
	}
	s.smu.Unlock()
	if ss == nil {
		labkit.Error(w, 404, "not_found", "no such playback session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- playlists ----------------------------------------------------------

func playlistHeaders(w http.ResponseWriter, maxAge int) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(maxAge))
}

func master(base, token string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, rd := range renditions {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s,CODECS=\"avc1.64001f,mp4a.40.2\"\n%s/%s/index.m3u8?token=%s\n", rd.Bandwidth, rd.Resolution, base, rd.Name, token)
	}
	return b.String()
}

func (s *server) vodMaster(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validVideo(id) {
		labkit.Error(w, 404, "not_found", "no such video")
		return
	}
	tok, ok := s.verify(w, r, "vod/"+id)
	if !ok {
		return
	}
	playlistHeaders(w, 3600)
	w.Write([]byte(master("/vod/"+id, tok)))
}

func segName(n int) string { return fmt.Sprintf("seg%05d.ts", n) }

func (s *server) vodMedia(w http.ResponseWriter, r *http.Request) {
	id, rd := r.PathValue("id"), findRendition(r.PathValue("rendition"))
	if !validVideo(id) || rd == nil {
		labkit.Error(w, 404, "not_found", "no such video or rendition")
		return
	}
	tok, ok := s.verify(w, r, "vod/"+id)
	if !ok {
		return
	}
	var b strings.Builder
	dur := s.segDur.Seconds()
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n", int(dur+0.999))
	for n := range Segments {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n/vod/%s/%s/%s?token=%s\n", dur, id, rd.Name, segName(n), tok)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	playlistHeaders(w, 3600)
	w.Write([]byte(b.String()))
}

func (s *server) liveMaster(w http.ResponseWriter, r *http.Request) {
	c := r.PathValue("channel")
	if !validChannel(c) {
		labkit.Error(w, 404, "not_found", "no such channel")
		return
	}
	tok, ok := s.verify(w, r, "live/"+c)
	if !ok {
		return
	}
	playlistHeaders(w, 60)
	w.Write([]byte(master("/live/"+c, tok)))
}

// liveWindow renders the live media playlist's segment lines (without the
// token) for the newest complete segment seq.
func (s *server) liveWindow(c string, rd *rendition, seq int) string {
	first := max(0, seq-window+1)
	var lines []string
	if s.cfg.Fixes.On("playlist") {
		for n := first; n <= seq; n++ {
			lines = append(lines, fmt.Sprintf("/live/%s/%s/%s", c, rd.Name, segName(n)))
		}
	} else {
		// Bottleneck: list every segment since the event began, as a
		// listing of the segment store would, and keep the newest.
		for n := 0; n <= seq; n++ {
			name := fmt.Sprintf("/live/%s/%s/%s", c, rd.Name, segName(n))
			if n >= first {
				lines = append(lines, name)
			}
		}
		s.listed.Add(seq + 1)
	}
	var b strings.Builder
	dur := s.segDur.Seconds()
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", int(dur+0.999), first)
	for _, l := range lines {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\x00\n", dur, l) // \x00 marks where the token goes
	}
	return b.String()
}

func (s *server) liveMedia(w http.ResponseWriter, r *http.Request) {
	c, rd := r.PathValue("channel"), findRendition(r.PathValue("rendition"))
	if !validChannel(c) || rd == nil {
		labkit.Error(w, 404, "not_found", "no such channel or rendition")
		return
	}
	tok, ok := s.verify(w, r, "live/"+c)
	if !ok {
		return
	}
	seq := s.live()
	var body string
	if s.cfg.Fixes.On("playlist") {
		key := c + "/" + rd.Name
		s.pmu.Lock()
		p, ok := s.playlists[key]
		if !ok || p.seq != seq {
			p = cachedPlaylist{seq: seq, body: s.liveWindow(c, rd, seq)}
			s.playlists[key] = p
		}
		s.pmu.Unlock()
		body = p.body
	} else {
		body = s.liveWindow(c, rd, seq)
	}
	playlistHeaders(w, 1)
	w.Write([]byte(strings.ReplaceAll(body, "\x00", "?token="+tok)))
}

// --- segments -----------------------------------------------------------

func segNumber(name string) (int, bool) {
	if !strings.HasPrefix(name, "seg") || !strings.HasSuffix(name, ".ts") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "seg"), ".ts"))
	return n, err == nil && n >= 0
}

func (s *server) vodSegment(w http.ResponseWriter, r *http.Request) {
	id, rd := r.PathValue("id"), findRendition(r.PathValue("rendition"))
	n, ok := segNumber(r.PathValue("segment"))
	if !validVideo(id) || rd == nil || !ok || n >= Segments {
		labkit.Error(w, 404, "not_found", "no such segment")
		return
	}
	if _, ok := s.verify(w, r, "vod/"+id); !ok {
		return
	}
	s.serveSegment(w, "vod/"+id, rd, n, "public, max-age=86400, immutable")
}

func (s *server) liveSegment(w http.ResponseWriter, r *http.Request) {
	c, rd := r.PathValue("channel"), findRendition(r.PathValue("rendition"))
	n, ok := segNumber(r.PathValue("segment"))
	if !validChannel(c) || rd == nil || !ok {
		labkit.Error(w, 404, "not_found", "no such segment")
		return
	}
	if _, ok := s.verify(w, r, "live/"+c); !ok {
		return
	}
	if seq := s.live(); n > seq || n < seq-60 {
		labkit.Error(w, 404, "not_found", "segment not available (live window)")
		return
	}
	s.serveSegment(w, "live/"+c, rd, n, "public, max-age=60")
}

func (s *server) serveSegment(w http.ResponseWriter, resource string, rd *rendition, n int, cc string) {
	key := fmt.Sprintf("%s/%s/%d", resource, rd.Name, n)
	var b []byte
	if s.cfg.Fixes.On("package") {
		b = s.cachedSegment(key, rd)
	} else {
		b = s.pack(key, rd) // bottleneck: package it again for every viewer
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", cc)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b) //nolint:gosec // MPEG-TS bytes from a validated segment key, served as video/mp2t, not HTML
}

func (s *server) cachedSegment(key string, rd *rendition) []byte {
	s.cmu.Lock()
	if e, ok := s.items[key]; ok {
		s.lru.MoveToFront(e)
		b := e.Value.(*lruItem).b
		s.cmu.Unlock()
		return b
	}
	s.cmu.Unlock()
	b := s.pack(key, rd)
	s.cmu.Lock()
	defer s.cmu.Unlock()
	if _, ok := s.items[key]; !ok {
		s.items[key] = s.lru.PushFront(&lruItem{key: key, b: b})
		s.bytes += len(b)
		for s.bytes > s.max {
			old := s.lru.Back()
			it := old.Value.(*lruItem)
			s.lru.Remove(old)
			delete(s.items, it.key)
			s.bytes -= len(it.b)
		}
	}
	return b
}

// pack muxes a segment's MPEG-TS packets: a sync byte, the packet id and a
// continuity counter in each header, deterministic payload, and a CRC
// over every packet, as a packager does.
func (s *server) pack(key string, rd *rendition) []byte {
	s.packaged.Add(1)
	size := rd.Bandwidth / 8 * int(2*time.Second/time.Second) / s.scale
	packets := max(1, size/tsPacket)
	out := make([]byte, packets*tsPacket)
	seed := crc32.ChecksumIEEE([]byte(key)) | 1
	var crc uint32
	for p := range packets {
		pkt := out[p*tsPacket : (p+1)*tsPacket]
		pkt[0], pkt[1], pkt[2], pkt[3] = 0x47, 0x41, 0x00, byte(0x10|p&0x0f)
		for i := 4; i < tsPacket; i++ {
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			pkt[i] = byte(seed)
		}
		crc = crc32.Update(crc, crc32.IEEETable, pkt)
	}
	// The last packet carries the running CRC, so the work cannot be skipped.
	tail := out[len(out)-4:]
	tail[0], tail[1], tail[2], tail[3] = byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc)
	return out
}
