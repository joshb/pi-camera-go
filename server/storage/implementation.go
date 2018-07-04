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

package storage

import (
	"fmt"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"strconv"
	"sync"
	"time"

	"github.com/joshb/pi-camera-go/server/util"
)

type storageImpl struct {
	segmentDir        string
	segmentDirMaxSize int64
	segments          map[SegmentID]Segment
	lastSegmentID     SegmentID
	mutex             *sync.Mutex
}

func New() (Storage, error) {
	segmentDir, err := util.ConfigDir("segments")
	if err != nil {
		return nil, err
	}

	segments, lastSegmentID, err := loadSegments(segmentDir)
	if err != nil {
		return nil, err
	}

	return &storageImpl{
		segmentDir: segmentDir,
		segmentDirMaxSize: 1024*1024*1024, // 1 GB
		segments: segments,
		lastSegmentID: lastSegmentID + 1,
		mutex: &sync.Mutex{},
	}, nil
}

func (s *storageImpl) SegmentDir() string {
	return s.segmentDir
}

func loadSegments(segmentDir string) (map[SegmentID]Segment, SegmentID, error) {
	// Get a listing of files in the segment directory.
	files, err := ioutil.ReadDir(segmentDir)
	if err != nil {
		return nil, 0, err
	}

	// Build a map of segments.
	segments := make(map[SegmentID]Segment, len(files))
	lastSegmentID := SegmentID(0)
	for _, fileInfo := range files {
		segment, err := segmentFromFileName(fileInfo.Name())
		if err == nil {
			segments[segment.ID] = segment
			if segment.ID > lastSegmentID {
				lastSegmentID = segment.ID
			}
		}
	}

	return segments, lastSegmentID, nil
}

func segmentFromFileName(name string) (Segment, error) {
	parts := strings.Split(strings.Split(name, ".")[0], "_")
	if len(parts) != 4 || parts[0] != "segment" {
		return Segment{}, errors.New("invalid segment file name")
	}

	// Parse segment time.
	segmentTime, err := strconv.Atoi(parts[1])
	if err != nil {
		return Segment{}, err
	}

	// Parse segment duration.
	segmentDuration, err := strconv.Atoi(parts[2])
	if err != nil {
		return Segment{}, err
	}

	// Parse segment ID.
	segmentID, err := strconv.Atoi(parts[3])
	if err != nil {
		return Segment{}, err
	}

	return Segment{
		ID:       SegmentID(segmentID),
		Name:     name,
		Time:     time.Unix(int64(segmentTime), 0),
		Duration: time.Duration(segmentDuration) * time.Millisecond,
	}, nil
}

func (s *storageImpl) LatestSegments(count int) []Segment {
	s.mutex.Lock()

	segments := make([]Segment, 0, count)
	lastSegmentID := s.lastSegmentID
	for segmentID := lastSegmentID - SegmentID(count) + 1; segmentID <= lastSegmentID; segmentID++ {
		if segment, ok := s.segments[segmentID]; ok {
			segments = append(segments, segment)
		}
	}

	s.mutex.Unlock()
	return segments
}

func (s *storageImpl) addSegment(filePath string, created, modified time.Time) error {
	t := time.Now()

	inFile, err := os.Open(filePath)
	if err != nil {
		return err
	}

	fileInfo, err := inFile.Stat()
	if err != nil {
		return err
	}

	segmentTime := created
	segmentDuration := modified.Sub(created)

	// Generate segment file name/path.
	segmentID := s.lastSegmentID + 1
	segmentName := fmt.Sprintf("segment_%d_%d_%d.ts", segmentTime.Unix(),
		(segmentDuration / time.Millisecond), segmentID)
	segmentPath := path.Join(s.segmentDir, segmentName)

	// Copy the file to the segments directory.
	outFile, err := os.Create(segmentPath)
	if err != nil {
		return err
	}
	if n, err := io.Copy(outFile, inFile); err != nil {
		return err
	} else if n != fileInfo.Size() {
		return errors.New("could not copy entire file")
	}

	s.mutex.Lock()
	s.lastSegmentID = segmentID
	s.segments[segmentID] = Segment{
		ID: segmentID,
		Name: segmentName,
		Time: segmentTime,
		Duration: segmentDuration,
	}
	s.mutex.Unlock()

	d := time.Since(t)
	println("Added segment", segmentID, "in", d / time.Millisecond, "ms")

	return nil
}

func (s *storageImpl) VideoRecorded(filePath string, created, modified time.Time) {
	if err := s.addSegment(filePath, created, modified); err != nil {
		fmt.Println("Error when adding segment:", err)
	}
}
