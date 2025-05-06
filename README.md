# jisu-pub-server
A Publication Manifest server based on the Readium [go-toolkit](https://github.com/readium/go-toolkit).  Server the manifest needed by the jisu-reader.

## Dev Setup
For development purposes it is possible to run this projects locally.  Manifests for EPUBs in the 'test' directory will be available.

Dependencies:  
go

### clone, build and test the pub server 

```
git clone https://github.com/nyudlts/jisu-pub-server --recursive
cd jisu-pub-server
go mod tidy
make install

//install the binary in your local 'go/bin' directory
cd cmd/rwp
go install 
cd ../..  
  
//start the server using the EPUBs in the 'test' directory
rwp serve test

//test in a local web browser
http://localhost:15080/list.json
http://localhost:15080/OTc4MTQ3OTgxOTQ5Mi5lcHVi/manifest.json
http://localhost:15080/OTc4MTQ3OTgxOTQ1NC5lcHVi/manifest.json
```

## Production Setup
For production, use the [jisu-build](https://github.com/nyudlts/jisu-build) project which includes this project as a submodule. 

## Adding ebooks to the Docker container
For production, the Dockerfile adds EPUBs served from GitHub pages via the 'docs' directory in [jisu-api](https://github.com/nyudlts/jisu-api) to the the Docker container as follows:  
```
ADD --chown=nonroot:nonroot https://nyudlts.github.io/jisu-api/9781479819454.epub /srv/publications/
ADD --chown=nonroot:nonroot https://nyudlts.github.io/jisu-api/9781479819492.epub /srv/publications/
```

To add additional EPUBs to the project, add them to the 'docs' folder in [jisu-api](https://github.com/nyudlts/jisu-api) and update the Dockerfile here to incluide them.  The 'test' directory is used for local testing only.

