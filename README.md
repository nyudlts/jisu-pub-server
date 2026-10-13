# jisu-pub-server

A Publication Manifest server based on the Readium [go-toolkit](https://github.com/readium/go-toolkit).  Serves the manifest needed by the [jisu-reader](https://github.com/nyudlts/jisu-reader).

## Dev Setup

For development purposes it is possible to run this project locally.  Manifests for EPUBs in the 'test' directory will be available.

Dependencies:  
- git
- docker (for devcontainer setup)
- go1.23.3
- `.epub` files for the publication

### clone, build and test the pub server locally

```
git clone https://github.com/nyudlts/jisu-pub-server --recursive
cd jisu-pub-server
go mod tidy
make install

//install the binary in your local 'go/bin' directory
cd cmd/rwp
go install 
cd ../..  

//start the server in local mode using the current workspace code
go run ./cmd/rwp serve --storage local --local-root test

//if you prefer an installed binary, rebuild/install first so flags are current
//(a stale /go/bin/rwp can miss newer flags like --storage)
go install ./cmd/rwp
rwp serve --storage local --local-root test

//test in a local web browser
http://localhost:15080/list.json
vol5
http://localhost:15080/OTc4MTQ3OTgxOTQ5Mi5lcHVi/manifest.json
vol3
http://localhost:15080/OTc4MTQ3OTgxOTQ1NC5lcHVi/manifest.json

// for remote book lookup when connected to the dlib.nyu file server
vol1
http://localhost:15080/OTc4MTQ3OTgzNzU1Ny5lcHVi/manifest.json
vol3
http://localhost:15080/OTc4MTQ3OTgxOTQ1NC5lcHVi/manifest.json
vol4
http://localhost:15080/OTc4MTQ3OTg0NDU2Mi5lcHVi/manifest.json
vol5
http://localhost:15080/OTc4MTQ3OTgxOTQ5Mi5lcHVi/manifest.json
```

## Testing

Preferred local verification commands:

- `make verify-local-ci` - Full local verification mirroring the manual CI workflow (`go build`, `go vet`, all tests, storage conformance, and integration in local mode).
- `make verify-storage-conformance` - Storage backend conformance coverage for local, HTTP, and S3 mock backends.
- `make verify-integration-local` - Integration tests pinned to local storage mode.

## Production Setup

For production, use the [jisu-build](https://github.com/nyudlts/jisu-build) project which includes this project as a submodule. 

## Storage Backends

Storage backend selection is explicit and fail-fast. Configure one mode at startup with `--storage` or `STORAGE`.

- Default mode: `http`
- No backend fallback: if required config is missing, startup returns an error naming the missing variable.
- No S3 credential flags: credentials are resolved only by the AWS SDK default credential chain.

### Required config by mode

- `http`: requires `STORAGE_BASE_URL` (or `--base-url`)
- `local`: requires `STORAGE_LOCAL_ROOT` (or `--local-root`)
- `s3`: requires `STORAGE_S3_BUCKET` (or `--s3-bucket`); optional `STORAGE_S3_REGION` / `STORAGE_S3_ENDPOINT`

See [docs/storage-backends.md](docs/storage-backends.md) for full operator examples and behavior notes.

## Adding ebooks to the Docker container

For production, the Dockerfile here adds EPUBs served via GitHub pages to its Docker container. The 'docs' directory in [jisu-api](https://github.com/nyudlts/jisu-api) is setup as a Docker Pages directory so any new EPUBs should be placed there.  Update the Dockerfile here to refelct any additonal EPUBs based on the following syntax:
```
ADD --chown=nonroot:nonroot https://nyudlts.github.io/jisu-api/9781479819454.epub /srv/publications/
ADD --chown=nonroot:nonroot https://nyudlts.github.io/jisu-api/9781479819492.epub /srv/publications/
```

The 'test' directory is used for local testing only.

## For me:

- [ ] add books locally instead of github pages + docker ADD
- [ ] try to obtain books from opensquare static filehosting
- [ ] add books locally instead of AWS S3 bucket + file fetching
- [ ] should the books be added via a submodule?

