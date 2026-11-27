package artistphoto

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
)

const (
	testArtistMBID       = "5b11f4ce-a62d-471e-81fc-a69a8278c7da"
	testQID              = "Q11649"
	testImageURL         = "https://thumb.wikimedia.org/wikipedia/commons/thumb/a/ab/Artist_photo.jpg/330px-Artist_photo.jpg"
	testOriginalImageURL = "https://upload.wikimedia.org/wikipedia/commons/a/ab/Artist_photo.jpg"
	testTrackedImageURL  = testOriginalImageURL + "?utm_source=commons.wikimedia.org&utm_campaign=imageinfo&utm_content=thumbnail_unscaled"
	testSourceURL        = "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg"
)

func TestResolvePrefersExplicitCommonsImageAndSanitizesAttribution(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("titles"); got != "File:Artist_photo.jpg" {
				t.Errorf("commons titles = %q", got)
			}
			if got := r.URL.Query().Get("iiurlwidth"); got != "330" {
				t.Errorf("commons thumbnail width = %q", got)
			}
			if got := r.URL.Query().Get("iiurlheight"); got != "330" {
				t.Errorf("commons thumbnail height = %q", got)
			}
			if got := r.Header.Get("User-Agent"); got != musicbrainz.UserAgent {
				t.Errorf("commons user agent = %q", got)
			}
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Errorf("commons accept = %q", got)
			}
			writeJSON(w, commonsPayload(commonsFile{
				Title:       "File:Artist_photo.jpg",
				Thumb:       testTrackedImageURL,
				URL:         testOriginalImageURL,
				Source:      testSourceURL,
				MIME:        "image/jpeg",
				Width:       320,
				Height:      200,
				ThumbWidth:  330,
				ThumbHeight: 206,
				Metadata: map[string]any{
					"Artist":           `<a href="https://example.invalid/artist">P.B. Rage</a> &amp; <b>friends</b>&nbsp;from USA`,
					"LicenseShortName": "CC BY-SA 2.0",
					"LicenseUrl":       "https://creativecommons.org/licenses/by-sa/2.0",
				},
			}))
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("wikidata should not be requested")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		},
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/artist/"+testArtistMBID {
			t.Errorf("musicbrainz path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("inc"); got != "url-rels" {
			t.Errorf("musicbrainz includes = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != musicbrainz.UserAgent {
			t.Errorf("musicbrainz user agent = %q", got)
		}
		writeJSON(w, musicBrainzArtist(
			relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg"),
			relation("wikidata", "https://www.wikidata.org/wiki/"+testQID),
		))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	want := Photo{
		ImageURL:   testOriginalImageURL,
		SourceURL:  testSourceURL,
		Author:     "P.B. Rage & friends from USA",
		License:    "CC BY-SA 2.0",
		LicenseURL: "https://creativecommons.org/licenses/by-sa/2.0",
	}
	if photo == nil || *photo != want {
		t.Fatalf("photo = %+v, want %+v", photo, want)
	}
	if api.commonsCalls.Load() != 1 || api.wikidataCalls.Load() != 0 {
		t.Fatalf("commons calls = %d, wikidata calls = %d", api.commonsCalls.Load(), api.wikidataCalls.Load())
	}
}

func TestResolvePrefersPreferredRankThenNormalWikidataP18(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("titles"); got != "File:Preferred.jpg" {
				t.Errorf("commons titles = %q", got)
			}
			writeJSON(w, commonsPayload(commonsFile{
				Title:  "File:Preferred.jpg",
				Thumb:  "https://upload.wikimedia.org/wikipedia/commons/thumb/c/cd/Preferred.jpg/640px-Preferred.jpg",
				URL:    "https://upload.wikimedia.org/wikipedia/commons/thumb/c/cd/Preferred.jpg/640px-Preferred.jpg",
				Source: "https://commons.wikimedia.org/wiki/File:Preferred.jpg",
				MIME:   "image/jpeg",
				Metadata: map[string]any{
					"Artist":           "Preferred Author",
					"LicenseShortName": "CC0",
				},
			}))
		},
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("action"); got != "wbgetclaims" {
				t.Errorf("wikidata action = %q", got)
			}
			if got := r.URL.Query().Get("entity"); got != testQID {
				t.Errorf("wikidata entity = %q", got)
			}
			if got := r.URL.Query().Get("property"); got != "P18" {
				t.Errorf("wikidata property = %q", got)
			}
			writeJSON(w, wikidataClaims(claim("normal", "Normal.jpg"), claim("preferred", "Preferred.jpg")))
		},
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("wikidata", "https://www.wikidata.org/wiki/"+testQID)))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.SourceURL != "https://commons.wikimedia.org/wiki/File:Preferred.jpg" || photo.Author != "Preferred Author" {
		t.Fatalf("photo = %+v", photo)
	}
	if api.commonsCalls.Load() != 1 || api.wikidataCalls.Load() != 1 {
		t.Fatalf("commons calls = %d, wikidata calls = %d", api.commonsCalls.Load(), api.wikidataCalls.Load())
	}
}

func TestResolveUsesNormalWikidataP18WhenPreferredIsAbsent(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("titles"); got != "File:Normal.jpg" {
				t.Errorf("commons titles = %q", got)
			}
			writeJSON(w, commonsPayload(commonsFile{
				Title:  "File:Normal.jpg",
				Thumb:  "https://upload.wikimedia.org/wikipedia/commons/thumb/e/ef/Normal.jpg/640px-Normal.jpg",
				URL:    "https://upload.wikimedia.org/wikipedia/commons/thumb/e/ef/Normal.jpg/640px-Normal.jpg",
				Source: "https://commons.wikimedia.org/wiki/File:Normal.jpg",
				MIME:   "image/png",
				Metadata: map[string]any{
					"Artist":           "Normal Author",
					"LicenseShortName": "CC BY 4.0",
				},
			}))
		},
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, wikidataClaims(claim("normal", "Normal.jpg")))
		},
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("wikidata", "https://www.wikidata.org/wiki/"+testQID)))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.SourceURL != "https://commons.wikimedia.org/wiki/File:Normal.jpg" || photo.License != "CC BY 4.0" {
		t.Fatalf("photo = %+v", photo)
	}
}

func TestResolveFallsBackToWikidataWhenExplicitCommonsFileIsMissing(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("titles") {
			case "File:Missing.jpg":
				writeJSON(w, missingCommonsPage("File:Missing.jpg"))
			case "File:Preferred.jpg":
				writeJSON(w, commonsPayload(commonsFile{
					Title:  "File:Preferred.jpg",
					Thumb:  "https://upload.wikimedia.org/wikipedia/commons/thumb/1/12/Preferred.jpg/640px-Preferred.jpg",
					URL:    "https://upload.wikimedia.org/wikipedia/commons/thumb/1/12/Preferred.jpg/640px-Preferred.jpg",
					Source: "https://commons.wikimedia.org/wiki/File:Preferred.jpg",
					MIME:   "image/jpeg",
					Metadata: map[string]any{
						"Artist":           "Preferred Author",
						"LicenseShortName": "CC BY-SA 4.0",
					},
				}))
			default:
				t.Errorf("unexpected commons title")
				http.Error(w, "unexpected title", http.StatusBadRequest)
			}
		},
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, wikidataClaims(claim("preferred", "Preferred.jpg")))
		},
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(
			relation("image", "https://commons.wikimedia.org/wiki/File:Missing.jpg"),
			relation("wikidata", "https://www.wikidata.org/wiki/"+testQID),
		))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.SourceURL != "https://commons.wikimedia.org/wiki/File:Preferred.jpg" {
		t.Fatalf("photo = %+v", photo)
	}
	if api.commonsCalls.Load() != 2 || api.wikidataCalls.Load() != 1 {
		t.Fatalf("commons calls = %d, wikidata calls = %d", api.commonsCalls.Load(), api.wikidataCalls.Load())
	}
}

func TestResolveReturnsNoPhotoForAbsentOrForeignRelations(t *testing.T) {
	cases := []struct {
		name      string
		relations []any
	}{
		{name: "absent", relations: nil},
		{name: "foreign image", relations: []any{relation("image", "https://evil.example/wiki/File:Bad.jpg")}},
		{name: "foreign wikidata", relations: []any{relation("wikidata", "https://evil.example/wiki/Q11649")}},
		{name: "non-file commons page", relations: []any{relation("image", "https://commons.wikimedia.org/wiki/Category:Artists")}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("commons should not be requested")
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				},
				func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("wikidata should not be requested")
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				},
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(testCase.relations...))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil || photo != nil {
				t.Fatalf("photo = %+v, err = %v", photo, err)
			}
			if api.commonsCalls.Load() != 0 || api.wikidataCalls.Load() != 0 {
				t.Fatalf("commons calls = %d, wikidata calls = %d", api.commonsCalls.Load(), api.wikidataCalls.Load())
			}
		})
	}
}

func TestResolveRejectsCrossHostRedirect(t *testing.T) {
	var redirectHits atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHits.Add(1)
		writeJSON(w, commonsPayload(commonsFile{
			Title:  "File:Redirected.jpg",
			Thumb:  "https://upload.wikimedia.org/wikipedia/commons/thumb/9/9a/Redirected.jpg/640px-Redirected.jpg",
			URL:    "https://upload.wikimedia.org/wikipedia/commons/thumb/9/9a/Redirected.jpg/640px-Redirected.jpg",
			Source: "https://commons.wikimedia.org/wiki/File:Redirected.jpg",
			MIME:   "image/jpeg",
		}))
	}))
	defer redirectTarget.Close()
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, redirectTarget.URL+"/redirected", http.StatusFound)
		},
		nil,
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err == nil || photo != nil {
		t.Fatalf("photo = %+v, err = %v", photo, err)
	}
	if redirectHits.Load() != 0 {
		t.Fatalf("cross-host redirect target was requested %d times", redirectHits.Load())
	}
}

func TestResolveRejectsUnallowlistedThumbnailURL(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, commonsPayload(commonsFile{
				Title:  "File:Artist_photo.jpg",
				Thumb:  "https://evil.example/photo.jpg",
				URL:    "https://evil.example/photo.jpg",
				Source: testSourceURL,
				MIME:   "image/jpeg",
			}))
		},
		nil,
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err == nil || photo != nil {
		t.Fatalf("photo = %+v, err = %v", photo, err)
	}
}

func TestResolveSkipsNonRasterCommonsFile(t *testing.T) {
	cases := []struct {
		name string
		mime string
		file string
	}{
		{name: "svg", mime: "image/svg+xml", file: "Artist_photo.svg"},
		{name: "pdf", mime: "application/pdf", file: "Artist_photo.pdf"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, commonsPayload(commonsFile{
						Title:  "File:" + testCase.file,
						Thumb:  "https://upload.wikimedia.org/wikipedia/commons/thumb/2/2b/" + testCase.file + "/640px-" + testCase.file + ".png",
						URL:    "https://upload.wikimedia.org/wikipedia/commons/thumb/2/2b/" + testCase.file + "/640px-" + testCase.file + ".png",
						Source: "https://commons.wikimedia.org/wiki/File:" + testCase.file,
						MIME:   testCase.mime,
					}))
				},
				nil,
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:"+testCase.file)))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil || photo != nil {
				t.Fatalf("photo = %+v, err = %v", photo, err)
			}
		})
	}
}

func TestResolveOmitsUnallowlistedLicenseURL(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, commonsPayload(commonsFile{
				Title:  "File:Artist_photo.jpg",
				Thumb:  testImageURL,
				URL:    testImageURL,
				Source: testSourceURL,
				MIME:   "image/jpeg",
				Metadata: map[string]any{
					"Artist":           "Author",
					"LicenseShortName": "CC0",
					"LicenseUrl":       "https://evil.example/license",
				},
			}))
		},
		nil,
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.License != "CC0" || photo.LicenseURL != "" {
		t.Fatalf("photo = %+v", photo)
	}
}

func TestResolveRejectsMissingOrUnknownLicense(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]any
	}{
		{name: "missing", metadata: nil},
		{name: "unknown short name", metadata: map[string]any{"Artist": "Author", "LicenseShortName": "Unknown"}},
		{name: "unknown machine name", metadata: map[string]any{"Artist": "Author", "License": "unknown"}},
		{name: "no license marker", metadata: map[string]any{"Artist": "Author", "LicenseShortName": "No license"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, commonsPayload(commonsFile{
						Title:    "File:Artist_photo.jpg",
						Thumb:    testImageURL,
						URL:      testImageURL,
						Source:   testSourceURL,
						MIME:     "image/jpeg",
						Metadata: testCase.metadata,
					}))
				},
				nil,
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil || photo != nil {
				t.Fatalf("photo = %+v, err = %v", photo, err)
			}
		})
	}
}

func TestResolveUsesKnownLicenseMetadataFallback(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, commonsPayload(commonsFile{
				Title:  "File:Artist_photo.jpg",
				Thumb:  testImageURL,
				URL:    testImageURL,
				Source: testSourceURL,
				MIME:   "image/jpeg",
				Metadata: map[string]any{
					"Artist":           "Author",
					"LicenseShortName": "Unknown",
					"License":          "cc-by-sa-4.0",
				},
			}))
		},
		nil,
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.License != "cc-by-sa-4.0" {
		t.Fatalf("photo = %+v", photo)
	}
}

func TestResolveRequiresUsableAuthorWhenAttributionRequired(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]any
		want     string
	}{
		{
			name:     "missing author",
			metadata: map[string]any{"AttributionRequired": "true", "LicenseShortName": "CC BY 4.0"},
		},
		{
			name:     "unknown author",
			metadata: map[string]any{"AttributionRequired": "true", "LicenseShortName": "CC BY 4.0", "Artist": "Unknown"},
		},
		{
			name:     "credit is not an author",
			metadata: map[string]any{"AttributionRequired": "true", "LicenseShortName": "CC BY 4.0", "Credit": "Credit Author"},
		},
		{
			name:     "not required",
			metadata: map[string]any{"AttributionRequired": "false", "LicenseShortName": "CC0"},
		},
		{
			name: "attribution overrides artist",
			metadata: map[string]any{
				"AttributionRequired": "true",
				"LicenseShortName":    "CC BY 4.0",
				"Attribution":         `<a href="https://example.invalid">Attribution</a> &amp; Author`,
				"Artist":              "Artist Author",
				"Credit":              "Credit Author",
			},
			want: "Attribution & Author",
		},
		{
			name:     "artist fallback",
			metadata: map[string]any{"AttributionRequired": "true", "LicenseShortName": "CC BY 4.0", "Artist": "Artist Author"},
			want:     "Artist Author",
		},
		{
			name: "unknown attribution falls back to artist",
			metadata: map[string]any{
				"AttributionRequired": "true",
				"LicenseShortName":    "CC BY 4.0",
				"Attribution":         "Unknown",
				"Artist":              "Artist Author",
			},
			want: "Artist Author",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, commonsPayload(commonsFile{
						Title:    "File:Artist_photo.jpg",
						Thumb:    testImageURL,
						URL:      testImageURL,
						Source:   testSourceURL,
						MIME:     "image/jpeg",
						Metadata: testCase.metadata,
					}))
				},
				nil,
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.want == "" && testCase.name != "not required" {
				if photo != nil {
					t.Fatalf("photo = %+v", photo)
				}
				return
			}
			if photo == nil || photo.Author != testCase.want {
				t.Fatalf("photo = %+v, want author %q", photo, testCase.want)
			}
		})
	}
}

func TestResolveNormalizesOfficialCCLicenseURL(t *testing.T) {
	cases := []struct {
		name        string
		metadata    map[string]any
		wantLicense string
		wantURL     string
	}{
		{
			name: "http CC normalized",
			metadata: map[string]any{
				"AttributionRequired": "true",
				"Artist":              "Author",
				"LicenseShortName":    "CC BY-SA 4.0",
				"LicenseUrl":          "http://creativecommons.org/licenses/by-sa/4.0",
			},
			wantLicense: "CC BY-SA 4.0",
			wantURL:     "https://creativecommons.org/licenses/by-sa/4.0",
		},
		{
			name: "public domain",
			metadata: map[string]any{
				"LicenseShortName": "Public domain",
				"LicenseUrl":       "http://creativecommons.org/publicdomain/mark/1.0/",
			},
			wantLicense: "Public domain",
			wantURL:     "https://creativecommons.org/publicdomain/mark/1.0/",
		},
		{
			name: "CC0",
			metadata: map[string]any{
				"LicenseShortName": "CC0",
				"LicenseUrl":       "http://creativecommons.org/publicdomain/zero/1.0/",
			},
			wantLicense: "CC0",
			wantURL:     "https://creativecommons.org/publicdomain/zero/1.0/",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, commonsPayload(commonsFile{
						Title:    "File:Artist_photo.jpg",
						Thumb:    testImageURL,
						URL:      testImageURL,
						Source:   testSourceURL,
						MIME:     "image/jpeg",
						Metadata: testCase.metadata,
					}))
				},
				nil,
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil {
				t.Fatal(err)
			}
			if photo == nil || photo.License != testCase.wantLicense || photo.LicenseURL != testCase.wantURL {
				t.Fatalf("photo = %+v", photo)
			}
		})
	}
}

func TestResolveUsesBoundedOriginalWhenThumbnailIsAbsent(t *testing.T) {
	originalURL := "https://upload.wikimedia.org/wikipedia/commons/a/ab/Artist_photo.jpg"
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, commonsPayload(commonsFile{
				Title:  "File:Artist_photo.jpg",
				URL:    originalURL,
				Source: testSourceURL,
				MIME:   "image/jpeg",
				Width:  400,
				Height: 300,
				Metadata: map[string]any{
					"Artist":           "Author",
					"LicenseShortName": "CC BY 4.0",
				},
			}))
		},
		nil,
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.ImageURL != originalURL {
		t.Fatalf("photo = %+v", photo)
	}
}

func TestResolveRejectsUnboundedOriginalFallback(t *testing.T) {
	cases := []struct {
		name   string
		width  int
		height int
	}{
		{name: "too large", width: 401, height: 300},
		{name: "too tall", width: 300, height: 401},
		{name: "missing height", width: 400},
		{name: "missing dimensions"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, commonsPayload(commonsFile{
						Title:  "File:Artist_photo.jpg",
						URL:    "https://upload.wikimedia.org/wikipedia/commons/a/ab/Artist_photo.jpg",
						Source: testSourceURL,
						MIME:   "image/jpeg",
						Width:  testCase.width,
						Height: testCase.height,
						Metadata: map[string]any{
							"Artist":           "Author",
							"LicenseShortName": "CC BY 4.0",
						},
					}))
				},
				nil,
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil || photo != nil {
				t.Fatalf("photo = %+v, err = %v", photo, err)
			}
		})
	}
}

func TestResolveAcceptsLegacyUploadThumbnailURL(t *testing.T) {
	legacyURL := "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Artist_photo.jpg/330px-Artist_photo.jpg"
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, commonsPayload(commonsFile{
				Title:    "File:Artist_photo.jpg",
				Thumb:    legacyURL,
				URL:      legacyURL,
				Source:   testSourceURL,
				MIME:     "image/jpeg",
				Metadata: map[string]any{"Artist": "Author", "LicenseShortName": "CC BY 4.0"},
			}))
		},
		nil,
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.ImageURL != legacyURL {
		t.Fatalf("photo = %+v", photo)
	}
}

func TestResolveRejectsOversizedOrUnreportedThumbnailDimensions(t *testing.T) {
	cases := []struct {
		name                string
		thumb               string
		width               int
		height              int
		thumbWidth          int
		thumbHeight         int
		omitThumbDimensions bool
	}{
		{name: "too wide", thumb: testImageURL, width: 330, height: 206, thumbWidth: 640, thumbHeight: 400},
		{name: "too tall", thumb: testImageURL, width: 206, height: 330, thumbWidth: 300, thumbHeight: 640},
		{name: "unreported", thumb: testImageURL, width: 330, height: 206, omitThumbDimensions: true},
		{name: "unscaled original too large", thumb: testTrackedImageURL, width: 401, height: 300, thumbWidth: 330, thumbHeight: 206},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, commonsPayload(commonsFile{
						Title:               "File:Artist_photo.jpg",
						Thumb:               testCase.thumb,
						URL:                 testImageURL,
						Source:              testSourceURL,
						MIME:                "image/jpeg",
						Width:               testCase.width,
						Height:              testCase.height,
						ThumbWidth:          testCase.thumbWidth,
						ThumbHeight:         testCase.thumbHeight,
						OmitThumbDimensions: testCase.omitThumbDimensions,
						Metadata:            map[string]any{"Artist": "Author", "LicenseShortName": "CC BY 4.0"},
					}))
				},
				nil,
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil || photo != nil {
				t.Fatalf("photo = %+v, err = %v", photo, err)
			}
		})
	}
}

func TestResolveHandlesWikidataWithoutP18OrMissingEntity(t *testing.T) {
	cases := []struct {
		name     string
		response any
	}{
		{name: "empty array claims", response: map[string]any{"claims": []any{}}},
		{name: "empty object claims", response: map[string]any{"claims": map[string]any{}}},
		{name: "empty P18", response: map[string]any{"claims": map[string]any{"P18": []any{}}}},
		{name: "missing entity", response: map[string]any{"error": map[string]any{"code": "no-such-entity"}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("commons should not be requested")
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				},
				func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, testCase.response)
				},
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("wikidata", "https://www.wikidata.org/wiki/"+testQID)))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err != nil || photo != nil {
				t.Fatalf("photo = %+v, err = %v", photo, err)
			}
			if api.commonsCalls.Load() != 0 || api.wikidataCalls.Load() != 1 {
				t.Fatalf("commons calls = %d, wikidata calls = %d", api.commonsCalls.Load(), api.wikidataCalls.Load())
			}
		})
	}
}

func TestResolveRejectsBadCommonsResponseEnvelope(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		status      int
		body        string
	}{
		{name: "bad status", contentType: "application/json", status: http.StatusBadGateway},
		{name: "bad content type", contentType: "text/html", status: http.StatusOK, body: "{}"},
		{name: "oversized", contentType: "application/json", status: http.StatusOK, body: strings.Repeat(" ", maxResponseBytes+1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			api := newAPIStub(t,
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", testCase.contentType)
					w.WriteHeader(testCase.status)
					_, _ = w.Write([]byte(testCase.body))
				},
				nil,
			)
			provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, musicBrainzArtist(relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg")))
			})

			photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
			if err == nil || photo != nil {
				t.Fatalf("photo = %+v, err = %v", photo, err)
			}
		})
	}
}

func TestResolveRejectsMusicBrainzResponseForAnotherArtist(t *testing.T) {
	api := newAPIStub(t,
		func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("commons should not be requested")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		},
		nil,
	)
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := json.Marshal(map[string]any{
			"id":   "11111111-1111-1111-1111-111111111111",
			"name": "Other Artist",
			"relations": []any{
				relation("image", "https://commons.wikimedia.org/wiki/File:Artist_photo.jpg"),
			},
		})
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})

	photo, err := api.resolver().Resolve(context.Background(), provider, testArtistMBID)
	if err == nil || photo != nil {
		t.Fatalf("photo = %+v, err = %v", photo, err)
	}
}

func TestResolveRejectsInvalidMBIDWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	provider, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	})
	api := newAPIStub(t, nil, nil)

	photo, err := api.resolver().Resolve(context.Background(), provider, "not-a-mbid")
	if err == nil || photo != nil {
		t.Fatalf("photo = %+v, err = %v", photo, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("musicbrainz was requested %d times", requests.Load())
	}
}

func TestPhotoJSONContract(t *testing.T) {
	data, err := json.Marshal(Photo{
		ImageURL:  "https://upload.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg",
		SourceURL: "https://commons.wikimedia.org/wiki/File:Photo.jpg",
		Author:    "Author",
		License:   "CC0",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"image_url":"https://upload.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg","source_url":"https://commons.wikimedia.org/wiki/File:Photo.jpg","author":"Author","license":"CC0"}`
	if string(data) != want {
		t.Fatalf("photo JSON = %s", data)
	}
}

func TestPlainTextStripsMarkupEntitiesAndControls(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "tags and entities", value: `<a href="https://example.invalid">A &amp; B</a><br>  C`, want: "A & B C"},
		{name: "escaped markup", value: `A &lt;script&gt;alert(1)&lt;/script&gt; B`, want: "A alert(1) B"},
		{name: "quoted tag delimiter", value: `A <span title="a > b">B</span> C`, want: "A B C"},
		{name: "controls", value: " A\x00B\tC\nD ", want: "A B C D"},
		{name: "empty", value: " \t\n ", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := plainText(testCase.value); got != testCase.want {
				t.Fatalf("plainText(%q) = %q, want %q", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestPlainTextBoundsAttribution(t *testing.T) {
	got := plainText(strings.Repeat("é", maxAttributionRunes+50))
	if len([]rune(got)) != maxAttributionRunes {
		t.Fatalf("attribution length = %d", len([]rune(got)))
	}
}

func TestURLValidationHelpers(t *testing.T) {
	if title, ok := commonsFileTitle("https://commons.wikimedia.org/wiki/File:Artist_photo.jpg"); !ok || title != "File:Artist_photo.jpg" {
		t.Fatalf("commons file title = %q, %v", title, ok)
	}
	for _, raw := range []string{
		"http://commons.wikimedia.org/wiki/File:Artist_photo.jpg",
		"https://evil.example/wiki/File:Artist_photo.jpg",
		"https://commons.wikimedia.org/wiki/Category:Artists",
		"https://commons.wikimedia.org/wiki/File:Artist_photo.jpg?uselang=en",
		"https://commons.wikimedia.org/wiki/File:Sub/Photo.jpg",
	} {
		if _, ok := commonsFileTitle(raw); ok {
			t.Fatalf("commons file title accepted %q", raw)
		}
	}
	if id, ok := wikidataEntityID("https://www.wikidata.org/wiki/Q11649"); !ok || id != testQID {
		t.Fatalf("wikidata entity = %q, %v", id, ok)
	}
	for _, raw := range []string{
		"http://www.wikidata.org/wiki/Q11649",
		"https://evil.example/wiki/Q11649",
		"https://www.wikidata.org/wiki/Q0",
		"https://www.wikidata.org/wiki/Property:P18",
	} {
		if _, ok := wikidataEntityID(raw); ok {
			t.Fatalf("wikidata entity accepted %q", raw)
		}
	}
	if imageURL, ok := normalizeThumbnailURL(testImageURL); !ok || imageURL != testImageURL {
		t.Fatalf("thumbnail URL = %q, %v", imageURL, ok)
	}
	if imageURL, ok := normalizeThumbnailURL(testTrackedImageURL); !ok || imageURL != testOriginalImageURL {
		t.Fatalf("tracked thumbnail URL = %q, %v", imageURL, ok)
	}
	trackedThumbnail := testImageURL + "?utm_source=commons.wikimedia.org&utm_campaign=imageinfo&utm_content=thumbnail"
	if imageURL, ok := normalizeThumbnailURL(trackedThumbnail); !ok || imageURL != testImageURL {
		t.Fatalf("tracked thumb.wikimedia.org URL = %q, %v", imageURL, ok)
	}
	legacyThumbnail := "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.jpg/640px-Photo.jpg"
	if imageURL, ok := normalizeThumbnailURL(legacyThumbnail); !ok || imageURL != legacyThumbnail {
		t.Fatalf("legacy thumbnail URL = %q, %v", imageURL, ok)
	}
	for _, raw := range []string{
		"http://thumb.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.jpg/640px-Photo.jpg",
		"https://evil.example/wikipedia/commons/thumb/a/ab/Photo.jpg/640px-Photo.jpg",
		"https://thumb.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg",
		"https://upload.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg",
		"https://thumb.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.svg",
		"https://thumb.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.jpg/640px-Photo.jpg?foo=bar",
	} {
		if _, ok := normalizeThumbnailURL(raw); ok {
			t.Fatalf("thumbnail URL accepted %q", raw)
		}
	}
	original := "https://upload.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg"
	if imageURL, ok := normalizeOriginalImageURL(original); !ok || imageURL != original {
		t.Fatalf("original image URL = %q, %v", imageURL, ok)
	}
	for _, raw := range []string{
		"http://upload.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg",
		"https://evil.example/wikipedia/commons/a/ab/Photo.jpg",
		"https://upload.wikimedia.org/wikipedia/en/a/ab/Photo.jpg",
		"https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.jpg/640px-Photo.jpg",
		"https://thumb.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg",
		"https://upload.wikimedia.org/wikipedia/commons/a/ab/Photo.svg",
	} {
		if _, ok := normalizeOriginalImageURL(raw); ok {
			t.Fatalf("original image URL accepted %q", raw)
		}
	}
	if sourceURL, ok := normalizeCommonsSourceURL(testSourceURL); !ok || sourceURL != testSourceURL {
		t.Fatalf("source URL = %q, %v", sourceURL, ok)
	}
	for _, raw := range []string{
		"http://commons.wikimedia.org/wiki/File:Photo.jpg",
		"https://evil.example/wiki/File:Photo.jpg",
		"https://commons.wikimedia.org/wiki/Category:Artists",
		"https://commons.wikimedia.org/wiki/File:Photo.jpg?uselang=en",
	} {
		if _, ok := normalizeCommonsSourceURL(raw); ok {
			t.Fatalf("source URL accepted %q", raw)
		}
	}
	for raw, want := range map[string]string{
		"https://creativecommons.org/licenses/by-sa/2.0":         "https://creativecommons.org/licenses/by-sa/2.0",
		"http://creativecommons.org/licenses/by-sa/2.0":          "https://creativecommons.org/licenses/by-sa/2.0",
		"http://creativecommons.org/publicdomain/zero/1.0/":      "https://creativecommons.org/publicdomain/zero/1.0/",
		"http://creativecommons.org/publicdomain/mark/1.0/":      "https://creativecommons.org/publicdomain/mark/1.0/",
		"https://creativecommons.org/licenses/by-sa/2.0/deed.en": "https://creativecommons.org/licenses/by-sa/2.0/deed.en",
	} {
		if got := normalizeLicenseURL(raw); got != want {
			t.Fatalf("normalizeLicenseURL(%q) = %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{
		"https://evil.example/licenses/by-sa/2.0",
		"https://creativecommons.org/licenses/by-sa/2.0?x=1",
		"https://creativecommons.org/other/by-sa/2.0",
	} {
		if got := normalizeLicenseURL(raw); got != "" {
			t.Fatalf("normalizeLicenseURL(%q) = %q", raw, got)
		}
	}
}

type apiStub struct {
	server          *httptest.Server
	commonsCalls    atomic.Int32
	wikidataCalls   atomic.Int32
	commonsHandler  http.HandlerFunc
	wikidataHandler http.HandlerFunc
}

func TestDownloadImageValidatesHostBoundsAndContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wikipedia/commons/thumb/a/ab/Photo.jpg/330px-Photo.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg bytes"))
		case "/wikipedia/commons/thumb/a/ab/Wrong.png/330px-Wrong.png":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		case "/wikipedia/commons/thumb/a/ab/Large.png/330px-Large.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(make([]byte, maxImageBytes+1))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	resolver := newResolver(server.Client(), "https://example.invalid/commons", "https://example.invalid/wikidata", server.Listener.Addr().String())

	image, err := resolver.DownloadImage(context.Background(), server.URL+"/wikipedia/commons/thumb/a/ab/Photo.jpg/330px-Photo.jpg")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if image.MIME != "image/jpeg" || string(image.Data) != "jpeg bytes" {
		t.Fatalf("image = %q %q", image.MIME, image.Data)
	}

	for _, test := range []struct {
		name string
		url  string
	}{
		{name: "foreign host", url: "https://example.invalid/wikipedia/commons/thumb/a/ab/Photo.jpg"},
		{name: "remote http", url: "http://upload.wikimedia.org/wikipedia/commons/a/ab/Photo.jpg"},
		{name: "unrelated path", url: server.URL + "/not-a-wikimedia-path.jpg"},
		{name: "query", url: server.URL + "/wikipedia/commons/thumb/a/ab/Photo.jpg/330px-Photo.jpg?download=1"},
		{name: "wrong content type", url: server.URL + "/wikipedia/commons/thumb/a/ab/Wrong.png/330px-Wrong.png"},
		{name: "oversized", url: server.URL + "/wikipedia/commons/thumb/a/ab/Large.png/330px-Large.png"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolver.DownloadImage(context.Background(), test.url); err == nil {
				t.Fatal("download succeeded, want an error")
			}
		})
	}
}

func TestDownloadImageRejectsCrossHostRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("redirected"))
	}))
	t.Cleanup(other.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/wikipedia/commons/a/ab/Target.png", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	resolver := newResolver(server.Client(), "https://example.invalid/commons", "https://example.invalid/wikidata", server.Listener.Addr().String())

	if _, err := resolver.DownloadImage(context.Background(), server.URL+"/wikipedia/commons/thumb/a/ab/Photo.png/330px-Photo.png"); err == nil {
		t.Fatal("cross-host redirect succeeded, want an error")
	}
}

func newAPIStub(t *testing.T, commonsHandler, wikidataHandler http.HandlerFunc) *apiStub {
	t.Helper()
	stub := &apiStub{commonsHandler: commonsHandler, wikidataHandler: wikidataHandler}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/commons/w/api.php":
			stub.commonsCalls.Add(1)
			if stub.commonsHandler == nil {
				http.NotFound(w, r)
				return
			}
			stub.commonsHandler(w, r)
		case "/wikidata/w/api.php":
			stub.wikidataCalls.Add(1)
			if stub.wikidataHandler == nil {
				http.NotFound(w, r)
				return
			}
			stub.wikidataHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *apiStub) resolver() *Resolver {
	host := s.server.Listener.Addr().String()
	return newResolver(s.server.Client(), s.server.URL+"/commons/w/api.php", s.server.URL+"/wikidata/w/api.php", host)
}

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*musicbrainz.Provider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("new musicbrainz provider: %v", err)
	}
	return provider, server
}

func musicBrainzArtist(relations ...any) map[string]any {
	if relations == nil {
		relations = []any{}
	}
	return map[string]any{
		"id":        testArtistMBID,
		"name":      "Test Artist",
		"relations": relations,
	}
}

func relation(kind, resource string) map[string]any {
	return map[string]any{
		"type":        kind,
		"target-type": "url",
		"direction":   "forward",
		"url":         map[string]any{"resource": resource},
	}
}

func claim(rank, filename string) map[string]any {
	return map[string]any{
		"rank": rank,
		"mainsnak": map[string]any{
			"snaktype": "value",
			"datavalue": map[string]any{
				"value": filename,
				"type":  "string",
			},
		},
	}
}

func wikidataClaims(claims ...any) map[string]any {
	return map[string]any{
		"claims": map[string]any{
			"P18": claims,
		},
	}
}

type commonsFile struct {
	Title               string
	Thumb               string
	URL                 string
	Source              string
	MIME                string
	Width               int
	Height              int
	ThumbWidth          int
	ThumbHeight         int
	OmitThumbDimensions bool
	Metadata            map[string]any
}

func commonsPayload(file commonsFile) map[string]any {
	metadata := map[string]any{}
	for key, value := range file.Metadata {
		metadata[key] = map[string]any{"value": value}
	}
	thumbWidth, thumbHeight := file.ThumbWidth, file.ThumbHeight
	if file.Thumb != "" && !file.OmitThumbDimensions {
		if thumbWidth == 0 {
			thumbWidth = thumbnailWidth
		}
		if thumbHeight == 0 {
			thumbHeight = thumbnailHeight
		}
	}
	imageInfo := map[string]any{
		"thumburl":       file.Thumb,
		"url":            file.URL,
		"descriptionurl": file.Source,
		"mime":           file.MIME,
		"width":          file.Width,
		"height":         file.Height,
		"thumbwidth":     thumbWidth,
		"thumbheight":    thumbHeight,
		"extmetadata":    metadata,
	}
	return map[string]any{
		"batchcomplete": true,
		"query": map[string]any{
			"pages": []any{
				map[string]any{
					"ns":        6,
					"title":     file.Title,
					"imageinfo": []any{imageInfo},
				},
			},
		},
	}
}

func missingCommonsPage(title string) map[string]any {
	return map[string]any{
		"batchcomplete": true,
		"query": map[string]any{
			"pages": []any{
				map[string]any{"ns": 6, "title": title, "missing": true},
			},
		},
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}
