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

package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/joshb/pi-camera-go/server/recorder"
	"github.com/joshb/pi-camera-go/server/storage"
	"github.com/joshb/pi-camera-go/server/util"
)

const (
	segmentsPrefix = "/segments/"
	staticPrefix = "/"
)

type Server interface {
	Start(addr string) error
	Stop() error
}

type serverImpl struct {
	privateKeyPath string
	publicKeyPath  string

	storage  storage.Storage
	recorder recorder.Recorder

	segmentsFileServer http.Handler
	staticFileServer   http.Handler
}

func New(https bool) (Server, error) {
	var privateKeyPath, publicKeyPath string
	if https {
		var err error
		privateKeyPath, publicKeyPath, err = util.KeyPaths()
		if err != nil {
			return nil, err
		}
	}

	return &serverImpl{
		privateKeyPath: privateKeyPath,
		publicKeyPath:  publicKeyPath,
	}, nil
}

func (s *serverImpl) Start(addr string) error {
	var err error
	s.storage, err = storage.New()
	if err != nil {
		return err
	}

	s.segmentsFileServer = http.StripPrefix(segmentsPrefix,
		http.FileServer(http.Dir(s.storage.SegmentDir())))
	s.staticFileServer = http.StripPrefix(staticPrefix,
		http.FileServer(http.Dir("static")))

	s.recorder, err = recorder.New()
	if err != nil {
		return err
	}

	if err := s.recorder.Start(); err != nil {
		fmt.Println("Unable to start recorder:", err)
		fmt.Println("Using mock recorder")
		s.recorder = recorder.NewMock()
		if err := s.recorder.Start(); err != nil {
			return err
		}
	}

	s.recorder.AddSubscriber(s.storage)

	println("Starting server at address", addr)
	if len(s.publicKeyPath) != 0 && len(s.privateKeyPath) != 0 {
		return http.ListenAndServeTLS(addr, s.publicKeyPath, s.privateKeyPath, s)
	} else {
		return http.ListenAndServe(addr, s)
	}
}

func (s *serverImpl) Stop() error {
	if err := s.recorder.Stop(); err != nil {
		return err
	}

	return nil
}

func (s *serverImpl) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	u := req.URL.String()
	if strings.HasPrefix(u, segmentsPrefix) {
		s.segmentsFileServer.ServeHTTP(w, req)
	} else if u == "/live.m3u" {
		s.serveLivePlaylist(w, false)
	} else if u == "/live.txt" {
		s.serveLivePlaylist(w, true)
	} else {
		s.staticFileServer.ServeHTTP(w, req)
	}
}

func (s *serverImpl) serveLivePlaylist(w http.ResponseWriter, txt bool) {
	// Get enough segments to fill 10 seconds.
	numSegments := int((10 * time.Second) / s.recorder.SegmentDuration())
	if numSegments < 3 {
		numSegments = 3
	}

	segments := s.storage.LatestSegments(numSegments)
	targetDuration := time.Duration(0)
	firstSegmentID := storage.SegmentID(0)
	for _, segment := range segments {
		if segment.Duration > targetDuration {
			targetDuration = segment.Duration
		}

		if firstSegmentID == 0 {
			firstSegmentID = segment.ID
		}
	}

	if txt {
		w.Header().Set("Content-Type", "text/plain")
	} else {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	}
	
	io.WriteString(w, "#EXTM3U\n")
	targetDurationInt := int(targetDuration / time.Second)
	io.WriteString(w, fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", targetDurationInt))
	io.WriteString(w, fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", firstSegmentID))

	prevSegmentID := firstSegmentID - 1
	for _, segment := range segments {
		// Indicate if there is a gap in segments.
		if segment.ID != prevSegmentID + 1 {
			io.WriteString(w, "#EXT-X-DISCONTINUITY\n")
		}

		duration := float64(segment.Duration) / float64(time.Second)
		io.WriteString(w, fmt.Sprintf("#EXTINF:%f,\n", duration))
		io.WriteString(w, fmt.Sprintf("segments/%s\n", segment.Name))

		prevSegmentID = segment.ID
	}
}