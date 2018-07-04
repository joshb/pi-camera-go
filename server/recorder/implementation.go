/*
 * Copyright (C) 2018 Josh A. Beam
 * All rights reserved.
 *
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions
 * are met:
 *   1. Redistributions of source code must retain the above copyright
 *      notice, this list of conditions and the following disclaimer.
 *   2. Redistributions in binary form must reproduce the above copyright
 *      notice, this list of conditions and the following disclaimer in the
 *      documentation and/or other materials provided with the distribution.
 *
 * THIS SOFTWARE IS PROVIDED BY THE AUTHOR ``AS IS'' AND ANY EXPRESS OR
 * IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES
 * OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
 * IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
 * SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
 * PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS;
 * OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY,
 * WHETHER IN CONTACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR
 * OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF
 * ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
 */

package recorder

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/joshb/pi-camera-go/server/util"
)

type recorderImpl struct {
	cancelFunc  context.CancelFunc
	cmd         *exec.Cmd

	recorderDir     string
	segmentDuration time.Duration
	width           int
	height          int
	bitRate         int

	subscribers []Subscriber
}

func New() (Recorder, error) {
	recorderDir, err := util.ConfigDir("recorder")
	if err != nil {
		return nil, err
	}

	return &recorderImpl{
		recorderDir:     recorderDir,
		segmentDuration: 5 * time.Second,
		width:           640,
		height:          480,
		bitRate:         4000000,
	}, nil
}

func (r *recorderImpl) muxFile(name string) (string, error) {
	t := time.Now()

	inPath := path.Join(r.recorderDir, name)
	newName := strings.Replace(name, ".h264", ".ts", 1)
	outPath := path.Join(r.recorderDir, newName)

	// Use ffmpeg to mux the file.
	args := []string{
		"-i", inPath,
		"-codec", "copy",
		outPath,
	}
	cmd := exec.Command("ffmpeg", args...)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Remove the input file.
	if err := os.Remove(inPath); err != nil {
		return "", err
	}

	d := time.Since(t)
	println("Created", outPath, "in", d / time.Millisecond, "ms")

	return outPath, nil
}

func (r *recorderImpl) deleteFiles() error {
	files, err := ioutil.ReadDir(r.recorderDir)
	if err != nil {
		return err
	}

	for _, fileInfo := range files {
		if !strings.HasSuffix(fileInfo.Name(), ".h264") {
			continue
		}

		filePath := path.Join(r.recorderDir, fileInfo.Name())
		if err := os.Remove(filePath); err != nil {
			return err
		}
	}

	return nil
}

func (r *recorderImpl) checkFiles() error {
	allFiles, err := ioutil.ReadDir(r.recorderDir)
	if err != nil {
		return err
	}

	// Build a list of .h264 files.
	files := make([]os.FileInfo, 0, len(allFiles))
	for _, fileInfo := range allFiles {
		if strings.HasSuffix(fileInfo.Name(), ".h264") {
			files = append(files, fileInfo)
		}
	}

	// If there are less than two files, we have nothing to do.
	filesLen := len(files)
	if filesLen < 2 {
		return nil
	}

	// Notify subscribers of any new video files and then remove them.
	for _, fileInfo := range files[:filesLen-1] {
		filePath, err := r.muxFile(fileInfo.Name())
		if err != nil {
			return err
		}

		created := time.Now()
		modified := created.Add(r.segmentDuration)
		for _, subscriber := range r.subscribers {
			subscriber.VideoRecorded(filePath, created, modified)
		}

		// Remove the file.
		if err := os.Remove(filePath); err != nil {
			return err
		}
	}

	return nil
}

func (r *recorderImpl) checkFilesLoop() {
	for r.cmd != nil {
		if err := r.checkFiles(); err != nil {
			fmt.Println("Error when checking files:", err)
		}

		time.Sleep(time.Second)
	}
}

func (r *recorderImpl) Start() error {
	if err := r.deleteFiles(); err != nil {
		return err
	}

	var ctx context.Context
	ctx, cancelFunc := context.WithCancel(context.Background())
	segmentPath := path.Join(r.recorderDir, "segment%012d.h264")
	args := []string{
		"--segment", strconv.Itoa(int(r.segmentDuration / time.Millisecond)),
		"--timeout", "0",
		"--width", strconv.Itoa(r.width),
		"--height", strconv.Itoa(r.height),
		"-b", strconv.Itoa(r.bitRate),
		"-o", segmentPath,
	}
	cmd := exec.CommandContext(ctx, "raspivid", args...)

	if err := cmd.Start(); err != nil {
		return err
	}

	r.cancelFunc = cancelFunc
	r.cmd = cmd

	go r.checkFilesLoop()
	return nil
}

func (r *recorderImpl) Stop() error {
	cancelFunc, cmd := r.cancelFunc, r.cmd
	r.cancelFunc, r.cmd = nil, nil
	cancelFunc()
	return cmd.Wait()
}

func (r *recorderImpl) SegmentDuration() time.Duration {
	return r.segmentDuration
}

func (r *recorderImpl) AddSubscriber(subscriber Subscriber) {
	r.subscribers = append(r.subscribers, subscriber)
}