package streamlab

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

func start(t *testing.T, fixes string) (*server, string) {
	s, h := newServer(labkit.Config{Fast: true, Fixes: labkit.ParseFixes(fixes)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func get(t *testing.T, u string) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func play(t *testing.T, base, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(base+"/api/playback", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if resp.StatusCode != 201 {
		t.Fatalf("playback: %d %v", resp.StatusCode, m)
	}
	return m
}

var uri = regexp.MustCompile(`(?m)^(/\S+)$`)

func TestVODPlayback(t *testing.T) {
	_, base := start(t, "")
	pb := play(t, base, `{"videoId":"v007"}`)
	code, h, master := get(t, base+pb["manifest"].(string))
	if code != 200 || h.Get("Content-Type") != "application/vnd.apple.mpegurl" || !bytes.Contains(master, []byte("#EXT-X-STREAM-INF:BANDWIDTH=400000")) {
		t.Fatalf("master: %d %s", code, master)
	}
	media := uri.FindAllString(string(master), -1)
	if len(media) != 4 {
		t.Fatalf("renditions: %v", media)
	}
	code, _, pl := get(t, base+media[1])
	segs := uri.FindAllString(string(pl), -1)
	if code != 200 || len(segs) != Segments || !bytes.HasSuffix(pl, []byte("#EXT-X-ENDLIST\n")) {
		t.Fatalf("media playlist: %d, %d segments", code, len(segs))
	}
	code, h, seg := get(t, base+segs[3])
	if code != 200 || h.Get("Content-Type") != "video/mp2t" || len(seg)%tsPacket != 0 || seg[0] != 0x47 || seg[tsPacket] != 0x47 {
		t.Fatalf("segment: %d %v %d bytes", code, h, len(seg))
	}
	// The same segment is the same bytes every time.
	if _, _, again := get(t, base+segs[3]); !bytes.Equal(seg, again) {
		t.Fatal("segment bytes changed")
	}
	if code, _, _ := get(t, base+strings.Split(segs[3], "?")[0]); code != 403 {
		t.Fatalf("segment without a token: %d", code)
	}
	if code, _, _ := get(t, base+strings.Replace(segs[3], "v007", "v008", 1)); code != 403 {
		t.Fatalf("another video's token: %d", code)
	}
}

func TestLivePlaylistSlides(t *testing.T) {
	for _, fixes := range []string{"", "playlist"} {
		_, base := start(t, fixes)
		pb := play(t, base, `{"channel":"live1"}`)
		_, _, master := get(t, base+pb["manifest"].(string))
		media := uri.FindAllString(string(master), -1)[1]
		_, _, a := get(t, base+media)
		time.Sleep(450 * time.Millisecond) // two fast-mode segments
		_, _, b := get(t, base+media)
		sa, sb := uri.FindAllString(string(a), -1), uri.FindAllString(string(b), -1)
		if len(sa) != window || len(sb) != window || sa[window-1] == sb[window-1] {
			t.Fatalf("fixes %q: the window did not move:\n%s\n%s", fixes, a, b)
		}
		if code, _, seg := get(t, base+sb[window-1]); code != 200 || len(seg) == 0 {
			t.Fatalf("newest live segment: %d", code)
		}
	}
}

func TestPackageBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "package"} {
		s, base := start(t, fixes)
		pb := play(t, base, `{"videoId":"v001"}`)
		tok := pb["token"].(string)
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if code, _, _ := get(t, base+"/vod/v001/480p/seg00000.ts?token="+tok); code != 200 {
					t.Errorf("status %d", code)
				}
			}()
		}
		wg.Wait()
		n := s.packaged.Load()
		if fixes == "" && n != 20 {
			t.Errorf("without the fix 20 viewers made %d packagings, want 20", n)
		}
		// Viewers arriving together may each miss the cache once, but a
		// later viewer always hits it.
		before := n
		get(t, base+"/vod/v001/480p/seg00000.ts?token="+tok)
		if fixes != "" && s.packaged.Load() != before {
			t.Errorf("with the fix a cached segment was packaged again")
		}
	}
}

func TestPlaylistBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "playlist"} {
		s, base := start(t, fixes)
		pb := play(t, base, `{"channel":"live2"}`)
		tok := pb["token"].(string)
		for range 10 {
			get(t, base+"/live/live2/480p/index.m3u8?token="+tok)
		}
		n := s.listed.Load()
		// The channels have been on air for an hour: thousands of segments.
		if fixes == "" && n < 10*int64(time.Hour/s.segDur) {
			t.Errorf("without the fix 10 playlists listed %d segments", n)
		}
		if fixes != "" && n != 0 {
			t.Errorf("with the fix the segment store was listed (%d)", n)
		}
	}
}

func TestStartupBottleneck(t *testing.T) {
	for _, fixes := range []string{"", "startup"} {
		s, base := start(t, fixes)
		play(t, base, `{"videoId":"v002"}`)
		if p := s.backends.Peak(); (fixes == "") != (p == 1) {
			t.Errorf("fixes %q: %d startup calls at once", fixes, p)
		}
	}
}
