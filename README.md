# gget - git-dumper sandbox
Creates a Docker ***Python*** image that installs [git-dumper](https://github.com/arthaud/git-dumper) and then runs a container with git-dumper **-u url** **-o outputDirectory**

## Why?
If the repository you are downloading is controlled by an attacker, this could lead to remote code execution on your machine.
So this creates a sandboxed environment for safe use

## prerequisites

* Docker Daemon CLI

## How to use 
```bash
$ go run main.go -u http://example.com/.git -o output/dir
```
OR build the program


### Notes on building

The build does embed a Tarfile into the program so any changes to the Dockerfile you should run

```bash
$ tar -cvf Dockerfile.tar Dockerfile
```

This is because the docker SDK expects a tarfile because just using a regular Dockerfile you get an EOF error
