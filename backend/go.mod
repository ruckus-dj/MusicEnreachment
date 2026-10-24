module github.com/ruckus/MusicEnreachment/backend

go 1.27.0

require (
	github.com/anyascii/go v0.3.3
	github.com/danielgtaylor/huma/v2 v2.39.1
	github.com/fergusstrange/embedded-postgres/v2 v2.0.0-alpha.3
	github.com/go-air/gini v1.0.4
	github.com/go-chi/chi/v5 v5.5.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/riverqueue/river v0.49.0
	github.com/riverqueue/river/riverdriver/riverdatabasesql v0.49.0
	github.com/riverqueue/river/rivertype v0.49.0
	github.com/ulikunitz/xz v0.5.17
	github.com/uptrace/bun v1.3.0
	github.com/uptrace/bun/dialect/pgdialect v1.3.0
	github.com/uptrace/bun/driver/pgdriver v1.3.0
	go.senan.xyz/taglib v0.0.0-20260926163408-32d7614f30ec
	go.uploadedlobster.com/mbtypes v0.4.0
	go.uploadedlobster.com/musicbrainzws2 v0.19.0
	golang.org/x/image v0.47.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	golang.org/x/time v0.16.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-resty/resty/v2 v2.17.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/lib/pq v1.12.3 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/riverqueue/river/riverdriver v0.49.0 // indirect
	github.com/riverqueue/river/riverdriver/riverpgxv5 v0.49.0 // indirect
	github.com/riverqueue/river/rivershared v0.49.0 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/tidwall/gjson v1.20.0 // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.2 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	mellium.im/sasl v0.3.2 // indirect
)

replace go.senan.xyz/taglib => github.com/deluan/go-taglib v0.0.0-20260926163408-32d7614f30ec
