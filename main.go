package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/docker/distribution/uuid"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/ttacon/chalk"
)

//go:embed Dockerfile.tar.gz
var tarFile []byte
var input, output string
var URLRegex = `https?:\/\/(www\.)?[-a-zA-Z0-9@:%._\+~#=]{1,256}\.[a-zA-Z0-9()]{1,6}\b([-a-zA-Z0-9()@:%_\+.~#?&//=]*)`

func main() {
	flag.StringVar(&input, "u", "", "git URL to download.")
	flag.StringVar(&output, "o", "", "output directory")
	flag.Parse()
	HandleInput(&input)
	HandleOutput(&output)

	ctx, cancel := context.WithCancel(context.Background())
	client, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		LogFatal("%s", "Unable to create client")
	}
	go HandleSIGTERM(func() {
		//Upon SIGTERM delete output dir and cancel context
		cancel()
		err := os.RemoveAll(output)
		if err != nil {
			LogFatal("%s", "Unable to remove output directory.")
		}
	})

	BuildImage(ctx, client)
	RunContainerThenRemove(ctx, client, CreateContainer(ctx, client))
}

//An object that implements io.Writer for git dumper log
type GitDumperLog struct {
	URLRegex *regexp.Regexp
}

func (g *GitDumperLog) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "Fetching") {
		fmt.Println(chalk.White.Color("(FETCHING)"), chalk.Green.Color(string(g.URLRegex.Find(p))))
	} else if strings.Contains(string(p), "Testing") {
		fmt.Println(chalk.White.Color("(TESTING)"), chalk.Yellow.Color(string(g.URLRegex.Find(p))))
	} else {
		fmt.Println(chalk.White.Color(string(p)))
	}
	return len(p), nil
}

//Runs a  created container by the given id then removes
func RunContainerThenRemove(ctxroot context.Context, client *client.Client, id string) {

	err := client.ContainerStart(ctxroot, id, types.ContainerStartOptions{})
	if err != nil {
		LogFatal("%s", "Unable to start container", id, err)
	}
	rc, err := client.ContainerLogs(ctxroot, id, types.ContainerLogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		LogFatal("%s", "Unable to follow container log output", err)
	}
	gdl := GitDumperLog{
		URLRegex: regexp.MustCompile(URLRegex),
	}
	io.Copy(&gdl, rc)

	client.ContainerRemove(ctxroot, id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})

	if err != nil {
		LogFatal("%s", "Unable to remove container", id, err)
	}
}

//Creates a contianer for gget to use
func CreateContainer(ctx context.Context, client *client.Client) (containerID string) {
	body, err := client.ContainerCreate(
		ctx,
		&container.Config{
			Image:        "gget",
			AttachStdout: true,
			AttachStderr: true,
			User:         "gget",
			//The entrypoint here is actually the invocation of the git-dumper command
			Cmd: []string{"git-dumper", input, "/home/gget"},
		},
		&container.HostConfig{
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: output,
					Target: "/home/gget",
				},
			},
		},
		nil,
		nil,
		//random uuid string for docker container name
		uuid.Generate().String(),
	)

	if err != nil {
		LogFatal("%s", "Unable to create a container", err)
	}
	return body.ID
}

//ImagePullResponse represents the output from docker's image build response.
//that implements io.Writer
type ImageBuildResponse struct {
	Stream      string      `json:"stream"`
	Status      string      `json:"status"`
	Progress    string      `json:"progress"`
	Aux         Aux         `json:"aux"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
	Error       string      `json:"error"`
}
type Aux struct {
	ID string `json:"id"`
}
type ErrorDetail struct {
	Message string `json:"message"`
}

func (ib *ImageBuildResponse) Write(p []byte) (int, error) {
	jd := json.NewDecoder(bytes.NewReader(p))
	err := jd.Decode(&ib)
	if ib.Error != "" {
		LogFatal("%s", ib.ErrorDetail.Message)
	}
	if ib.Stream != "" {
		fmt.Println(chalk.White.Color("(STREAM)"))
		fmt.Println(chalk.Green.Color(ib.Stream))
	}
	if ib.Progress != "" {
		fmt.Println(chalk.White.Color("(STATUS)"))
		fmt.Println(chalk.Green.Color(ib.Status))
	}
	if ib.Status != "" {
		fmt.Println(chalk.White.Color("(PROGRESS)"))
		fmt.Println(chalk.Green.Color(ib.Progress))
	}
	return len(p), err
}

//Build an image from embedded tar file
func BuildImage(ctx context.Context, client *client.Client) {
	var ibr ImageBuildResponse
	options := types.ImageBuildOptions{
		Tags: []string{"gget"},
	}
	res, err := client.ImageBuild(ctx, bytes.NewReader(tarFile), options)
	if err != nil {
		LogFatal("%s", "Unable to build image", err)
	}
	//Discard written bytes
	_, err = io.Copy(&ibr, res.Body)
	if err != nil {
		LogFatal("%s", "Unable to build copy build response", err)
	}
}

//handles the input URL
func HandleInput(input *string) {
	if input == nil || *input == "" {
		LogFatal("%s", errors.New("The URL must be specified -u URL"))
	}
}

//Handles the creation of the output directory
func HandleOutput(output *string) {
	if output == nil || *output == "" {
		LogFatal("%s", errors.New("The output directory must be specified -o DIR"))
	}
	// if output begins with the tilde ~
	// get the users home directory
	s := *output
	if string(s[0]) == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			LogFatal("%s", err, output)
		}
		*output = strings.Replace(*output, "~", home, 1)
	}
	if !path.IsAbs(*output) {
		if abs, err := filepath.Abs(*output); err != nil {
			LogFatal("%s", err, output)
		} else {
			*output = abs
		}
	}
	err := os.MkdirAll(*output, os.ModePerm)
	if err != nil {
		LogFatal("%s", err, output)
	}

}

//Starts a channel listening for SIGTERM Ctrl+C and invokes the callback
func HandleSIGTERM(cb func()) {
	//cleanup func upon Ctrl+C SIGINT or SIGTERM
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cb()
		os.Exit(1)
	}()
}

//Prints and exits
func LogFatal(spec string, v ...any) {
	var format string
	for i := 0; i < len(v); i++ {
		format += (spec + " ")
	}
	output := fmt.Sprintf(format, v...)
	log.Fatal(chalk.White.Color("(ERROR) "), chalk.Red.Color(output))
}
