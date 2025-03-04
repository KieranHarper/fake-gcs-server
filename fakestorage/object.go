// Copyright 2017 Francisco Souza. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fakestorage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/fsouza/fake-gcs-server/internal/backend"
	"github.com/fsouza/fake-gcs-server/internal/notification"
	"github.com/gorilla/mux"
)

var errInvalidGeneration = errors.New("invalid generation ID")

// ObjectAttrs returns only the meta-data about an object without its contents.
type ObjectAttrs struct {
	BucketName      string
	Name            string
	Size            int64
	ContentType     string
	ContentEncoding string
	// Crc32c checksum of Content. calculated by server when it's upload methods are used.
	Crc32c  string
	Md5Hash string
	Etag    string
	ACL     []storage.ACLRule
	// Dates and generation can be manually injected, so you can do assertions on them,
	// or let us fill these fields for you
	Created    time.Time
	Updated    time.Time
	Deleted    time.Time
	Generation int64
	Metadata   map[string]string
}

func (o *ObjectAttrs) id() string {
	return o.BucketName + "/" + o.Name
}

// Object represents the object that is stored within the fake server.
type Object struct {
	ObjectAttrs
	Content []byte
}

// MarshalJSON for Object to use ACLRule instead of storage.ACLRule
func (o Object) MarshalJSON() ([]byte, error) {
	temp := struct {
		BucketName      string            `json:"bucket"`
		Name            string            `json:"name"`
		Size            int64             `json:"size,string"`
		ContentType     string            `json:"contentType"`
		ContentEncoding string            `json:"contentEncoding"`
		Content         []byte            `json:"-"`
		Crc32c          string            `json:"crc32c,omitempty"`
		Md5Hash         string            `json:"md5Hash,omitempty"`
		Etag            string            `json:"etag,omitempty"`
		ACL             []aclRule         `json:"acl,omitempty"`
		Created         time.Time         `json:"created,omitempty"`
		Updated         time.Time         `json:"updated,omitempty"`
		Deleted         time.Time         `json:"deleted,omitempty"`
		Generation      int64             `json:"generation,omitempty,string"`
		Metadata        map[string]string `json:"metadata,omitempty"`
	}{
		BucketName:      o.BucketName,
		Name:            o.Name,
		ContentType:     o.ContentType,
		ContentEncoding: o.ContentEncoding,
		Size:            o.Size,
		Content:         o.Content,
		Crc32c:          o.Crc32c,
		Md5Hash:         o.Md5Hash,
		Etag:            o.Etag,
		Created:         o.Created,
		Updated:         o.Updated,
		Deleted:         o.Deleted,
		Generation:      o.Generation,
		Metadata:        o.Metadata,
	}
	temp.ACL = make([]aclRule, len(o.ACL))
	for i, ACL := range o.ACL {
		temp.ACL[i] = aclRule(ACL)
	}
	return json.Marshal(temp)
}

// UnmarshalJSON for Object to use ACLRule instead of storage.ACLRule
func (o *Object) UnmarshalJSON(data []byte) error {
	temp := struct {
		BucketName      string            `json:"bucket"`
		Name            string            `json:"name"`
		Size            int64             `json:"size,string"`
		ContentType     string            `json:"contentType"`
		ContentEncoding string            `json:"contentEncoding"`
		Content         []byte            `json:"-"`
		Crc32c          string            `json:"crc32c,omitempty"`
		Md5Hash         string            `json:"md5Hash,omitempty"`
		Etag            string            `json:"etag,omitempty"`
		ACL             []aclRule         `json:"acl,omitempty"`
		Created         time.Time         `json:"created,omitempty"`
		Updated         time.Time         `json:"updated,omitempty"`
		Deleted         time.Time         `json:"deleted,omitempty"`
		Generation      int64             `json:"generation,omitempty,string"`
		Metadata        map[string]string `json:"metadata,omitempty"`
	}{}
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}
	o.BucketName = temp.BucketName
	o.Name = temp.Name
	o.ContentType = temp.ContentType
	o.ContentEncoding = temp.ContentEncoding
	o.Size = temp.Size
	o.Content = temp.Content
	o.Crc32c = temp.Crc32c
	o.Md5Hash = temp.Md5Hash
	o.Etag = temp.Etag
	o.Created = temp.Created
	o.Updated = temp.Updated
	o.Deleted = temp.Deleted
	o.Generation = temp.Generation
	o.Metadata = temp.Metadata
	o.ACL = make([]storage.ACLRule, len(temp.ACL))
	for i, ACL := range temp.ACL {
		o.ACL[i] = storage.ACLRule(ACL)
	}

	return nil
}

// ACLRule is an alias of storage.ACLRule to have custom JSON marshal
type aclRule storage.ACLRule

// ProjectTeam is an alias of storage.ProjectTeam to have custom JSON marshal
type projectTeam storage.ProjectTeam

// MarshalJSON for ACLRule to customize field names
func (acl aclRule) MarshalJSON() ([]byte, error) {
	temp := struct {
		Entity      storage.ACLEntity `json:"entity"`
		EntityID    string            `json:"entityId"`
		Role        storage.ACLRole   `json:"role"`
		Domain      string            `json:"domain"`
		Email       string            `json:"email"`
		ProjectTeam *projectTeam      `json:"projectTeam"`
	}{
		Entity:      acl.Entity,
		EntityID:    acl.EntityID,
		Role:        acl.Role,
		Domain:      acl.Domain,
		Email:       acl.Email,
		ProjectTeam: (*projectTeam)(acl.ProjectTeam),
	}
	return json.Marshal(temp)
}

// UnmarshalJSON for ACLRule to customize field names
func (acl *aclRule) UnmarshalJSON(data []byte) error {
	temp := struct {
		Entity      storage.ACLEntity `json:"entity"`
		EntityID    string            `json:"entityId"`
		Role        storage.ACLRole   `json:"role"`
		Domain      string            `json:"domain"`
		Email       string            `json:"email"`
		ProjectTeam *projectTeam      `json:"projectTeam"`
	}{}
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}
	acl.Entity = temp.Entity
	acl.EntityID = temp.EntityID
	acl.Role = temp.Role
	acl.Domain = temp.Domain
	acl.Email = temp.Email
	acl.ProjectTeam = (*storage.ProjectTeam)(temp.ProjectTeam)
	return nil
}

// MarshalJSON for ProjectTeam to customize field names
func (team projectTeam) MarshalJSON() ([]byte, error) {
	temp := struct {
		ProjectNumber string `json:"projectNumber"`
		Team          string `json:"team"`
	}{
		ProjectNumber: team.ProjectNumber,
		Team:          team.Team,
	}
	return json.Marshal(temp)
}

// UnmarshalJSON for ProjectTeam to customize field names
func (team *projectTeam) UnmarshalJSON(data []byte) error {
	temp := struct {
		ProjectNumber string `json:"projectNumber"`
		Team          string `json:"team"`
	}{}
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}
	team.ProjectNumber = temp.ProjectNumber
	team.Team = temp.Team
	return nil
}

type objectAttrsList []ObjectAttrs

func (o objectAttrsList) Len() int {
	return len(o)
}

func (o objectAttrsList) Less(i int, j int) bool {
	return o[i].Name < o[j].Name
}

func (o *objectAttrsList) Swap(i int, j int) {
	d := *o
	d[i], d[j] = d[j], d[i]
}

// CreateObject stores the given object internally.
//
// If the bucket within the object doesn't exist, it also creates it. If the
// object already exists, it overrides the object.
func (s *Server) CreateObject(obj Object) {
	_, err := s.createObject(obj)
	if err != nil {
		panic(err)
	}
}

func (s *Server) createObject(obj Object) (Object, error) {
	oldBackendObj, err := s.backend.GetObject(obj.BucketName, obj.Name)
	prevVersionExisted := err == nil

	newBackendObj, err := s.backend.CreateObject(toBackendObjects([]Object{obj})[0])
	if err != nil {
		return Object{}, err
	}

	var newObjEventAttr map[string]string
	if prevVersionExisted {
		newObjEventAttr = map[string]string{
			"overwroteGeneration": strconv.FormatInt(oldBackendObj.Generation, 10),
		}

		oldObjEventAttr := map[string]string{
			"overwrittenByGeneration": strconv.FormatInt(newBackendObj.Generation, 10),
		}

		bucket, _ := s.backend.GetBucket(obj.BucketName)
		if bucket.VersioningEnabled {
			s.eventManager.Trigger(&oldBackendObj, notification.EventArchive, oldObjEventAttr)
		} else {
			s.eventManager.Trigger(&oldBackendObj, notification.EventDelete, oldObjEventAttr)
		}
	}

	newObj := fromBackendObjects([]backend.Object{newBackendObj})[0]
	s.eventManager.Trigger(&newBackendObj, notification.EventFinalize, newObjEventAttr)
	return newObj, nil
}

type ListOptions struct {
	Prefix                   string
	Delimiter                string
	Versions                 bool
	StartOffset              string
	EndOffset                string
	IncludeTrailingDelimiter bool
}

// ListObjects returns a sorted list of objects that match the given criteria,
// or an error if the bucket doesn't exist.
//
// Deprecated: use ListObjectsWithOptions.
func (s *Server) ListObjects(bucketName, prefix, delimiter string, versions bool) ([]ObjectAttrs, []string, error) {
	return s.ListObjectsWithOptions(bucketName, ListOptions{
		Prefix:    prefix,
		Delimiter: delimiter,
		Versions:  versions,
	})
}

func (s *Server) ListObjectsWithOptions(bucketName string, options ListOptions) ([]ObjectAttrs, []string, error) {
	backendObjects, err := s.backend.ListObjects(bucketName, options.Prefix, options.Versions)
	if err != nil {
		return nil, nil, err
	}
	objects := fromBackendObjectsAttrs(backendObjects)
	olist := objectAttrsList(objects)
	sort.Sort(&olist)
	var respObjects []ObjectAttrs
	prefixes := make(map[string]bool)
	for _, obj := range olist {
		if !strings.HasPrefix(obj.Name, options.Prefix) {
			continue
		}
		objName := strings.Replace(obj.Name, options.Prefix, "", 1)
		delimPos := strings.Index(objName, options.Delimiter)
		if options.Delimiter != "" && delimPos > -1 {
			prefix := obj.Name[:len(options.Prefix)+delimPos+1]
			if isInOffset(prefix, options.StartOffset, options.EndOffset) {
				prefixes[prefix] = true
			}
			if options.IncludeTrailingDelimiter && obj.Name == prefix {
				respObjects = append(respObjects, obj)
			}
		} else {
			if isInOffset(obj.Name, options.StartOffset, options.EndOffset) {
				respObjects = append(respObjects, obj)
			}
		}
	}
	respPrefixes := make([]string, 0, len(prefixes))
	for p := range prefixes {
		respPrefixes = append(respPrefixes, p)
	}
	sort.Strings(respPrefixes)
	return respObjects, respPrefixes, nil
}

func isInOffset(name, startOffset, endOffset string) bool {
	if endOffset != "" && startOffset != "" {
		return strings.Compare(name, endOffset) < 0 && strings.Compare(name, startOffset) >= 0
	} else if endOffset != "" {
		return strings.Compare(name, endOffset) < 0
	} else if startOffset != "" {
		return strings.Compare(name, startOffset) >= 0
	} else {
		return true
	}
}

func getCurrentIfZero(date time.Time) time.Time {
	if date.IsZero() {
		return time.Now()
	}
	return date
}

func toBackendObjects(objects []Object) []backend.Object {
	backendObjects := make([]backend.Object, 0, len(objects))
	for _, o := range objects {
		backendObjects = append(backendObjects, backend.Object{
			ObjectAttrs: backend.ObjectAttrs{
				BucketName:      o.BucketName,
				Name:            o.Name,
				Size:            int64(len(o.Content)),
				ContentType:     o.ContentType,
				ContentEncoding: o.ContentEncoding,
				Crc32c:          o.Crc32c,
				Md5Hash:         o.Md5Hash,
				Etag:            o.Etag,
				ACL:             o.ACL,
				Created:         getCurrentIfZero(o.Created).Format(timestampFormat),
				Deleted:         o.Deleted.Format(timestampFormat),
				Updated:         getCurrentIfZero(o.Updated).Format(timestampFormat),
				Generation:      o.Generation,
				Metadata:        o.Metadata,
			},
			Content: o.Content,
		})
	}
	return backendObjects
}

func fromBackendObjects(objects []backend.Object) []Object {
	backendObjects := make([]Object, 0, len(objects))
	for _, o := range objects {
		backendObjects = append(backendObjects, Object{
			ObjectAttrs: ObjectAttrs{
				BucketName:      o.BucketName,
				Name:            o.Name,
				Size:            int64(len(o.Content)),
				ContentType:     o.ContentType,
				ContentEncoding: o.ContentEncoding,
				Crc32c:          o.Crc32c,
				Md5Hash:         o.Md5Hash,
				Etag:            o.Etag,
				ACL:             o.ACL,
				Created:         convertTimeWithoutError(o.Created),
				Deleted:         convertTimeWithoutError(o.Deleted),
				Updated:         convertTimeWithoutError(o.Updated),
				Generation:      o.Generation,
				Metadata:        o.Metadata,
			},
			Content: o.Content,
		})
	}
	return backendObjects
}

func fromBackendObjectsAttrs(objectAttrs []backend.ObjectAttrs) []ObjectAttrs {
	oattrs := make([]ObjectAttrs, 0, len(objectAttrs))
	for _, o := range objectAttrs {
		oattrs = append(oattrs, ObjectAttrs{
			BucketName:      o.BucketName,
			Name:            o.Name,
			Size:            o.Size,
			ContentType:     o.ContentType,
			ContentEncoding: o.ContentEncoding,
			Crc32c:          o.Crc32c,
			Md5Hash:         o.Md5Hash,
			Etag:            o.Etag,
			ACL:             o.ACL,
			Created:         convertTimeWithoutError(o.Created),
			Deleted:         convertTimeWithoutError(o.Deleted),
			Updated:         convertTimeWithoutError(o.Updated),
			Generation:      o.Generation,
			Metadata:        o.Metadata,
		})
	}
	return oattrs
}

func convertTimeWithoutError(t string) time.Time {
	r, _ := time.Parse(timestampFormat, t)
	return r
}

// GetObject returns the object with the given name in the given bucket, or an
// error if the object doesn't exist.
func (s *Server) GetObject(bucketName, objectName string) (Object, error) {
	backendObj, err := s.backend.GetObject(bucketName, objectName)
	if err != nil {
		return Object{}, err
	}
	obj := fromBackendObjects([]backend.Object{backendObj})[0]
	return obj, nil
}

// GetObjectWithGeneration returns the object with the given name and given
// generation ID in the given bucket, or an error if the object doesn't exist.
//
// If versioning is enabled, archived versions are considered.
func (s *Server) GetObjectWithGeneration(bucketName, objectName string, generation int64) (Object, error) {
	backendObj, err := s.backend.GetObjectWithGeneration(bucketName, objectName, generation)
	if err != nil {
		return Object{}, err
	}
	obj := fromBackendObjects([]backend.Object{backendObj})[0]
	return obj, nil
}

func (s *Server) objectWithGenerationOnValidGeneration(bucketName, objectName, generationStr string) (Object, error) {
	generation, err := strconv.ParseInt(generationStr, 10, 64)
	if err != nil && generationStr != "" {
		return Object{}, errInvalidGeneration
	} else if generation > 0 {
		return s.GetObjectWithGeneration(bucketName, objectName, generation)
	}
	return s.GetObject(bucketName, objectName)
}

func (s *Server) listObjects(r *http.Request) jsonResponse {
	bucketName := mux.Vars(r)["bucketName"]
	objs, prefixes, err := s.ListObjectsWithOptions(bucketName, ListOptions{
		Prefix:                   r.URL.Query().Get("prefix"),
		Delimiter:                r.URL.Query().Get("delimiter"),
		Versions:                 r.URL.Query().Get("versions") == "true",
		StartOffset:              r.URL.Query().Get("startOffset"),
		EndOffset:                r.URL.Query().Get("endOffset"),
		IncludeTrailingDelimiter: r.URL.Query().Get("includeTrailingDelimiter") == "true",
	})
	if err != nil {
		return jsonResponse{status: http.StatusNotFound}
	}
	return jsonResponse{data: newListObjectsResponse(objs, prefixes)}
}

func (s *Server) getObject(w http.ResponseWriter, r *http.Request) {
	if alt := r.URL.Query().Get("alt"); alt == "media" || r.Method == http.MethodHead {
		s.downloadObject(w, r)
		return
	}

	handler := jsonToHTTPHandler(func(r *http.Request) jsonResponse {
		vars := mux.Vars(r)

		obj, err := s.objectWithGenerationOnValidGeneration(vars["bucketName"], vars["objectName"], r.FormValue("generation"))
		if err != nil {
			statusCode := http.StatusNotFound
			var errMessage string
			if errors.Is(err, errInvalidGeneration) {
				statusCode = http.StatusBadRequest
				errMessage = err.Error()
			}
			return jsonResponse{
				status:       statusCode,
				errorMessage: errMessage,
			}
		}
		header := make(http.Header)
		header.Set("Accept-Ranges", "bytes")
		return jsonResponse{
			header: header,
			data:   newObjectResponse(obj.ObjectAttrs),
		}
	})

	handler(w, r)
}

func (s *Server) deleteObject(r *http.Request) jsonResponse {
	vars := mux.Vars(r)
	obj, err := s.GetObject(vars["bucketName"], vars["objectName"])
	if err == nil {
		err = s.backend.DeleteObject(vars["bucketName"], vars["objectName"])
	}
	if err != nil {
		return jsonResponse{status: http.StatusNotFound}
	}
	bucket, _ := s.backend.GetBucket(obj.BucketName)
	backendObj := toBackendObjects([]Object{obj})[0]
	if bucket.VersioningEnabled {
		s.eventManager.Trigger(&backendObj, notification.EventArchive, nil)
	} else {
		s.eventManager.Trigger(&backendObj, notification.EventDelete, nil)
	}
	return jsonResponse{}
}

func (s *Server) listObjectACL(r *http.Request) jsonResponse {
	vars := mux.Vars(r)

	obj, err := s.GetObject(vars["bucketName"], vars["objectName"])
	if err != nil {
		return jsonResponse{status: http.StatusNotFound}
	}

	return jsonResponse{data: newACLListResponse(obj.ObjectAttrs)}
}

func (s *Server) setObjectACL(r *http.Request) jsonResponse {
	vars := mux.Vars(r)

	obj, err := s.GetObject(vars["bucketName"], vars["objectName"])
	if err != nil {
		return jsonResponse{status: http.StatusNotFound}
	}

	var data struct {
		Entity string
		Role   string
	}

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&data); err != nil {
		return jsonResponse{
			status:       http.StatusBadRequest,
			errorMessage: err.Error(),
		}
	}

	entity := storage.ACLEntity(data.Entity)
	role := storage.ACLRole(data.Role)
	obj.ACL = []storage.ACLRule{{
		Entity: entity,
		Role:   role,
	}}

	_, err = s.createObject(obj)
	if err != nil {
		return errToJsonResponse(err)
	}

	return jsonResponse{data: newACLListResponse(obj.ObjectAttrs)}
}

func (s *Server) rewriteObject(r *http.Request) jsonResponse {
	vars := mux.Vars(r)
	obj, err := s.objectWithGenerationOnValidGeneration(vars["sourceBucket"], vars["sourceObject"], r.FormValue("sourceGeneration"))
	if err != nil {
		statusCode := http.StatusNotFound
		var errMessage string
		if errors.Is(err, errInvalidGeneration) {
			statusCode = http.StatusBadRequest
			errMessage = err.Error()
		}
		return jsonResponse{errorMessage: errMessage, status: statusCode}
	}

	var metadata multipartMetadata
	err = json.NewDecoder(r.Body).Decode(&metadata)
	if err != nil && err != io.EOF { // The body is optional
		return jsonResponse{errorMessage: "Invalid metadata", status: http.StatusBadRequest}
	}

	// Only supplied metadata overwrites the new object's metdata
	if len(metadata.Metadata) == 0 {
		metadata.Metadata = obj.Metadata
	}
	if metadata.ContentType == "" {
		metadata.ContentType = obj.ContentType
	}
	if metadata.ContentEncoding == "" {
		metadata.ContentEncoding = obj.ContentEncoding
	}

	dstBucket := vars["destinationBucket"]
	newObject := Object{
		ObjectAttrs: ObjectAttrs{
			BucketName:      dstBucket,
			Name:            vars["destinationObject"],
			Size:            int64(len(obj.Content)),
			Crc32c:          obj.Crc32c,
			Md5Hash:         obj.Md5Hash,
			ACL:             obj.ACL,
			ContentType:     metadata.ContentType,
			ContentEncoding: metadata.ContentEncoding,
			Metadata:        metadata.Metadata,
		},
		Content: append([]byte(nil), obj.Content...),
	}

	_, err = s.createObject(newObject)
	if err != nil {
		return errToJsonResponse(err)
	}

	return jsonResponse{data: newObjectRewriteResponse(newObject.ObjectAttrs)}
}

func (s *Server) downloadObject(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	obj, err := s.objectWithGenerationOnValidGeneration(vars["bucketName"], vars["objectName"], r.FormValue("generation"))
	if err != nil {
		statusCode := http.StatusNotFound
		message := http.StatusText(statusCode)
		if errors.Is(err, errInvalidGeneration) {
			statusCode = http.StatusBadRequest
			message = err.Error()
		}
		http.Error(w, message, statusCode)
		return
	}

	status := http.StatusOK
	ranged, start, lastByte, content, satisfiable := s.handleRange(obj, r)

	if ranged && satisfiable {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, lastByte, len(obj.Content)))
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("X-Goog-Generation", strconv.FormatInt(obj.Generation, 10))
	w.Header().Set("X-Goog-Hash", fmt.Sprintf("crc32c=%s,md5=%s", obj.Crc32c, obj.Md5Hash))
	w.Header().Set("Last-Modified", obj.Updated.Format(http.TimeFormat))

	if ranged && !satisfiable {
		status = http.StatusRequestedRangeNotSatisfiable
		content = []byte(fmt.Sprintf(`<?xml version='1.0' encoding='UTF-8'?>`+
			`<Error><Code>InvalidRange</Code>`+
			`<Message>The requested range cannot be satisfied.</Message>`+
			`<Details>%s</Details></Error>`, r.Header.Get("Range")))
		w.Header().Set(contentTypeHeader, "application/xml; charset=UTF-8")
	} else {
		if obj.ContentType != "" {
			w.Header().Set(contentTypeHeader, obj.ContentType)
		}
		if obj.ContentEncoding != "" {
			w.Header().Set("Content-Encoding", obj.ContentEncoding)
		}
	}

	w.WriteHeader(status)
	if r.Method == http.MethodGet {
		w.Write(content)
	}
}

func (s *Server) handleRange(obj Object, r *http.Request) (ranged bool, start int64, lastByte int64, content []byte, satisfiable bool) {
	contentLength := int64(len(obj.Content))
	start, end, err := parseRange(r.Header.Get("Range"), contentLength)
	if err != nil {
		// If the range isn't valid, GCS returns all content.
		return false, 0, 0, obj.Content, false
	}
	// GCS is pretty flexible when it comes to invalid ranges. A 416 http
	// response is only returned when the range start is beyond the length of
	// the content. Otherwise, the range is ignored.
	switch {
	// Invalid start. Return 416 and NO content.
	// Examples:
	//   Length: 40, Range: bytes=50-60
	//   Length: 40, Range: bytes=50-
	case start >= contentLength:
		// This IS a ranged request, but it ISN'T satisfiable.
		return true, 0, 0, []byte{}, false
	// Negative range, ignore range and return all content.
	// Examples:
	//   Length: 40, Range: bytes=30-20
	case end < start:
		return false, 0, 0, obj.Content, false
	// Return range. Clamp start and end.
	// Examples:
	//   Length: 40, Range: bytes=-100
	//   Length: 40, Range: bytes=0-100
	default:
		if start < 0 {
			start = 0
		}
		if end >= contentLength {
			end = contentLength - 1
		}
		return true, start, end, obj.Content[start : end+1], true
	}
}

// parseRange parses the range header and returns the corresponding start and
// end indices in the content. The end index is inclusive. This function
// doesn't validate that the start and end indices fall within the content
// bounds. The content length is only used to handle "suffix length" and
// range-to-end ranges.
func parseRange(rangeHeaderValue string, contentLength int64) (start int64, end int64, err error) {
	// For information about the range header, see:
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Range
	// https://httpwg.org/specs/rfc7233.html#header.range
	// https://httpwg.org/specs/rfc7233.html#byte.ranges
	// https://httpwg.org/specs/rfc7233.html#status.416
	//
	// <unit>=<range spec>
	//
	// The following ranges are parsed:
	// "bytes=40-50" (range with given start and end)
	// "bytes=40-"   (range to end of content)
	// "bytes=-40"   (suffix length, offset from end of string)
	//
	// The unit MUST be "bytes".
	parts := strings.SplitN(rangeHeaderValue, "=", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expecting `=` in range header, got: %s", rangeHeaderValue)
	}
	if parts[0] != "bytes" {
		return 0, 0, fmt.Errorf("invalid range unit, expecting `bytes`, got: %s", parts[0])
	}
	rangeSpec := parts[1]
	if len(rangeSpec) == 0 {
		return 0, 0, errors.New("empty range")
	}
	if rangeSpec[0] == '-' {
		offsetFromEnd, err := strconv.ParseInt(rangeSpec, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid suffix length, got: %s", rangeSpec)
		}
		start = contentLength + offsetFromEnd
		end = contentLength - 1
	} else {
		rangeParts := strings.SplitN(rangeSpec, "-", 2)
		if len(rangeParts) != 2 {
			return 0, 0, fmt.Errorf("only one range supported, got: %s", rangeSpec)
		}
		start, err = strconv.ParseInt(rangeParts[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range start, got: %s", rangeParts[0])
		}
		if rangeParts[1] == "" {
			end = contentLength - 1
		} else {
			end, err = strconv.ParseInt(rangeParts[1], 10, 64)
			if err != nil {
				return 0, 0, fmt.Errorf("invalid range end, got: %s", rangeParts[1])
			}
		}
	}
	return start, end, nil
}

func (s *Server) patchObject(r *http.Request) jsonResponse {
	vars := mux.Vars(r)
	bucketName := vars["bucketName"]
	objectName := vars["objectName"]
	var metadata struct {
		Metadata map[string]string `json:"metadata"`
	}
	err := json.NewDecoder(r.Body).Decode(&metadata)
	if err != nil {
		return jsonResponse{
			status:       http.StatusBadRequest,
			errorMessage: "Metadata in the request couldn't decode",
		}
	}
	backendObj, err := s.backend.PatchObject(bucketName, objectName, metadata.Metadata)
	if err != nil {
		return jsonResponse{
			status:       http.StatusNotFound,
			errorMessage: "Object not found to be PATCHed",
		}
	}

	s.eventManager.Trigger(&backendObj, notification.EventMetadata, nil)
	return jsonResponse{data: fromBackendObjects([]backend.Object{backendObj})[0]}
}

func (s *Server) updateObject(r *http.Request) jsonResponse {
	vars := mux.Vars(r)
	bucketName := vars["bucketName"]
	objectName := vars["objectName"]
	var metadata struct {
		Metadata map[string]string `json:"metadata"`
	}
	err := json.NewDecoder(r.Body).Decode(&metadata)
	if err != nil {
		return jsonResponse{
			status:       http.StatusBadRequest,
			errorMessage: "Metadata in the request couldn't decode",
		}
	}
	backendObj, err := s.backend.UpdateObject(bucketName, objectName, metadata.Metadata)
	if err != nil {
		return jsonResponse{
			status:       http.StatusNotFound,
			errorMessage: "Object not found to be updated",
		}
	}

	s.eventManager.Trigger(&backendObj, notification.EventMetadata, nil)
	return jsonResponse{data: fromBackendObjects([]backend.Object{backendObj})[0]}
}

func (s *Server) composeObject(r *http.Request) jsonResponse {
	vars := mux.Vars(r)
	bucketName := vars["bucketName"]
	destinationObject := vars["destinationObject"]

	var composeRequest struct {
		SourceObjects []struct {
			Name string
		}
		Destination struct {
			Bucket      string
			ContentType string
			Metadata    map[string]string
		}
	}

	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&composeRequest)
	if err != nil {
		return jsonResponse{
			status:       http.StatusBadRequest,
			errorMessage: "Error parsing request body",
		}
	}

	sourceNames := make([]string, 0, len(composeRequest.SourceObjects))
	for _, n := range composeRequest.SourceObjects {
		sourceNames = append(sourceNames, n.Name)
	}

	backendObj, err := s.backend.ComposeObject(bucketName, sourceNames, destinationObject, composeRequest.Destination.Metadata, composeRequest.Destination.ContentType)
	if err != nil {
		return jsonResponse{
			status:       http.StatusInternalServerError,
			errorMessage: "Error running compose",
		}
	}

	obj := fromBackendObjects([]backend.Object{backendObj})[0]

	s.eventManager.Trigger(&backendObj, notification.EventFinalize, nil)

	return jsonResponse{data: newObjectResponse(obj.ObjectAttrs)}
}
