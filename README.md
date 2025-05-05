# NYU Go Server
A Dockerized go server for serving eBook web pub manifests.  
Based on the Readium go-toolkit.  
Served from AWS EC2.  

## Test the NYU-Go-Server Locally
Here are the steps to test the go-server to make sure it is working correctly before adding it to a Docker container.

Clone nyu-go-server from GitHub  
Add books for testing to the nyu-go-server/test directory  
'Make' the project and 'install' it for local execution.

```
git clone https://github.com/BluefireProductions/nyu-go-server.git --recursive
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




