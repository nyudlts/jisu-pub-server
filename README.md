# jisu-pub-server
A Publication Manifest server based on the Readium go-toolkit.  

## Test the NYU-Go-Server Locally
For development purposes it is possible to run this projects locally.

Clone nyu-go-server recursively from GitHub  

```
git clone  https://github.com/BluefireProductions/nyu-press-go.git --recursive
cd nyu-go-server
go mod tidy
make install

//build the binary in the local 'go/bin' directory
cd cmd/rwp
go install 
cd ../..
rwp serve test

//test in a local web browser
http://localhost:15080/list.json
http://localhost:15080/OTc4MTQ3OTgxOTQ5Mi5lcHVi/manifest.json
http://localhost:9000/OTc4MTQ3OTgxOTQ1NC5lcHVi/manifest.json
```

## Adding ebooks to the container
For producion, add links to eBooks to be included in the Docker container.  
Dockerfile:

```
ADD --chown=nonroot:nonroot https://bluefireproductions.github.io/jisu-epubs/9781479819454.epub /srv/publications/
ADD --chown=nonroot:nonroot https://bluefireproductions.github.io/jisu-epubs/9781479819492.epub /srv/publications/
```




