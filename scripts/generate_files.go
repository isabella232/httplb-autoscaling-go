// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Binary main uses the provided service account key to duplicate all of
// the files in the indicated bucket. It uses several concurrent copiers and
// provides for a naive retry mechanism.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/storage/v1"
)

const (
	numCopiers = 10
	numFiles   = 1000
)

var (
	keyFile   = flag.String("key-file", "", "The path to the user's service account JSON key.")
	imageFile = flag.String("image-file", "", "The path to the image file to duplicate in GCS.")
	bucket    = flag.String("bucket", "", "The bucket in which to generate files.")
)

type GCSCopyReq struct {
	SourceBucket, SourceFile, DestBucket, DestFile string
}

func buildName(prefix int, name string) string {
	return strings.Join([]string{strconv.Itoa(prefix), name}, "-")
}

// copyObjects takes copy requests from the input channel and attempts to use
// the GCS Storage API to perform the action. It incorporates naive retry logic
// and will output failures to the outut channel.
func copyObjects(s *storage.Service, in <-chan *GCSCopyReq, out chan<- string) {
	var err error
	for o := range in {
		for i := 0; i < 3; i++ {
			if _, err = s.Objects.Copy(o.SourceBucket, o.SourceFile, o.DestBucket, o.DestFile, nil).Do(); err == nil {
				break
			}
		}
		if err != nil {
			out <- o.DestFile
		}
	}
}

func main() {
	flag.Parse()
	file, err := os.Open(*imageFile)
	if err != nil {
		log.Fatalf("Error opening image file: %v", err)
	}
	fileName := path.Base(*imageFile)
	defer file.Close()
	bytes, err := ioutil.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("Error reading key file: %v", err)
	}
	conf, err := google.JWTConfigFromJSON(bytes, storage.DevstorageFull_controlScope)
	if err != nil {
		log.Fatalf("Could not build JWT config: %v", err)
	}
	service, err := storage.New(conf.Client(oauth2.NoContext))
	if err != nil {
		log.Fatalf("Failed to create GCS client: %v", err)
	}
	// Insert the image into GCS.
	baseFileName := buildName(0, fileName)
	_, err = service.Objects.Insert(*bucket, &storage.Object{Name: baseFileName}).Media(file).Do()
	if err != nil {
		log.Fatalf("Unable to upload initial file to bucket: %v", err)
	}
	c := make(chan *GCSCopyReq, 999)
	f := make(chan string)
	wg := &sync.WaitGroup{}
	wg.Add(numCopiers)
	for i := 0; i < numCopiers; i++ {
		go func() {
			copyObjects(service, c, f)
			wg.Done()
		}()
	}
	go func() {
		wg.Wait()
		close(f)
	}()
	for i := 1; i < numFiles; i++ {
		c <- &GCSCopyReq{
			SourceBucket: *bucket,
			SourceFile:   baseFileName,
			DestBucket:   *bucket,
			DestFile:     buildName(i, fileName),
		}
	}
	close(c)
	for errFile := range f {
		fmt.Printf("Could not copy to %v\n", errFile)
	}
}
