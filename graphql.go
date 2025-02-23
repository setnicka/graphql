// Package graphql provides a low level GraphQL client.
//
//	// create a client (safe to share across requests)
//	client := graphql.NewClient("https://machinebox.io/graphql")
//
//	// make a request
//	req := graphql.NewRequest(`
//		query ($key: String!) {
//			items (id:$key) {
//				field1
//				field2
//				field3
//			}
//		}
//	`)
//
//	// set any variables
//	req.Var("key", "value")
//
//	// run it and capture the response
//	var respData ResponseStruct
//	if err := client.Run(ctx, req, &respData); err != nil {
//		log.Fatal(err)
//	}
//
// # Specify client
//
// To specify your own http.Client, use the WithHTTPClient option:
//
//	httpclient := &http.Client{}
//	client := graphql.NewClient("https://machinebox.io/graphql", graphql.WithHTTPClient(httpclient))
package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/pkg/errors"
)

// Client is a client for interacting with a GraphQL API.
type Client struct {
	endpoint         string
	httpClient       *http.Client
	useMultipartForm bool

	// closeReq will close the request body immediately allowing for reuse of client
	closeReq bool

	// Log is called with various debug information.
	// To log to standard out, use:
	//  client.Log = func(s string) { log.Println(s) }
	Log func(s string)
}

// NewClient makes a new Client capable of making GraphQL requests.
func NewClient(endpoint string, opts ...ClientOption) *Client {
	c := &Client{
		endpoint: endpoint,
		Log:      func(string) {},
	}
	for _, optionFunc := range opts {
		optionFunc(c)
	}
	if c.httpClient == nil {
		c.httpClient = http.DefaultClient
	}
	return c
}

func (c *Client) logf(format string, args ...interface{}) {
	c.Log(fmt.Sprintf(format, args...))
}

// Run executes the query and unmarshals the response from the data field
// into the response object.
// Pass in a nil response object to skip response parsing.
// If the request fails or the server returns an error, the returned error will
// be of type Errors. Type assert to get the underlying errors:
//
//	err := client.Run(..)
//	if err != nil {
//		if gqlErrors, ok := err.(graphql.Errors); ok {
//			for _, e := range gqlErrors {
//				// Server returned an error
//			}
//		}
//		// Another error occurred
//	}
func (c *Client) Run(ctx context.Context, req *Request, resp interface{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if len(req.files) > 0 && !c.useMultipartForm {
		return errors.New("cannot send files with PostFields option")
	}
	if c.useMultipartForm {
		return c.runWithPostFields(ctx, req, resp)
	}
	return c.runWithJSON(ctx, req, resp)
}

func (c *Client) runWithJSON(ctx context.Context, req *Request, resp interface{}) error {
	var requestBody bytes.Buffer
	requestBodyObj := struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}{
		Query:     req.q,
		Variables: req.vars,
	}
	if err := json.NewEncoder(&requestBody).Encode(requestBodyObj); err != nil {
		return errors.Wrap(err, "encode body")
	}
	c.logf(">> variables: %v", req.vars)
	c.logf(">> query: %s", req.q)
	gr := &graphResponse{
		Data: resp,
	}
	r, err := http.NewRequest(http.MethodPost, c.endpoint, &requestBody)
	if err != nil {
		return err
	}
	r.Close = c.closeReq
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	r.Header.Set("Accept", "application/json; charset=utf-8")
	for key, values := range req.Header {
		for _, value := range values {
			r.Header.Add(key, value)
		}
	}
	c.logf(">> headers: %v", r.Header)
	r = r.WithContext(ctx)
	res, err := c.httpClient.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, res.Body); err != nil {
		return errors.Wrap(err, "reading body")
	}
	c.logf("<< %s", buf.String())
	bodyStr := buf.String()

	if err := json.NewDecoder(&buf).Decode(&gr); err != nil {
		if res.StatusCode != http.StatusOK {
			return errors.Errorf("graphql: server returned a non-200 status code: %v", res.StatusCode)
		}
		return errors.Wrap(err, "decoding response")
	}
	if len(gr.Errors) > 0 {
		return gr.Errors
	}

	// Handle the http status codes before handling response from the graphql endpoint.
	// If this is not done then !200 status codes will just be ignored without the caller even noticing, instead
	// the caller just gets back an empty result set, suggesting that the query did not found any result.
	// The reason for this is that for example in case of a 404,401,403 etc. the request is rejected before
	// it even hits an graphql handler on the server side.
	// As a result the response returned by this non graphql component is not compliant to the graphql spec.
	if res.StatusCode != http.StatusOK {
		return errors.Errorf("graphql: server returned a non-200 status code: %v - %v", res.StatusCode, bodyStr)
	}

	return nil
}

func (c *Client) runWithPostFields(ctx context.Context, req *Request, resp interface{}) error {
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	if err := writer.WriteField("query", req.q); err != nil {
		return errors.Wrap(err, "write query field")
	}
	var variablesBuf bytes.Buffer
	if len(req.vars) > 0 {
		variablesField, err := writer.CreateFormField("variables")
		if err != nil {
			return errors.Wrap(err, "create variables field")
		}
		if err := json.NewEncoder(io.MultiWriter(variablesField, &variablesBuf)).Encode(req.vars); err != nil {
			return errors.Wrap(err, "encode variables")
		}
	}
	for i := range req.files {
		part, err := writer.CreateFormFile(req.files[i].Field, req.files[i].Name)
		if err != nil {
			return errors.Wrap(err, "create form file")
		}
		if _, err := io.Copy(part, req.files[i].R); err != nil {
			return errors.Wrap(err, "preparing file")
		}
	}
	if err := writer.Close(); err != nil {
		return errors.Wrap(err, "close writer")
	}
	c.logf(">> variables: %s", variablesBuf.String())
	c.logf(">> files: %d", len(req.files))
	c.logf(">> query: %s", req.q)
	gr := &graphResponse{
		Data: resp,
	}
	r, err := http.NewRequest(http.MethodPost, c.endpoint, &requestBody)
	if err != nil {
		return err
	}
	r.Close = c.closeReq
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Accept", "application/json; charset=utf-8")
	for key, values := range req.Header {
		for _, value := range values {
			r.Header.Add(key, value)
		}
	}
	c.logf(">> headers: %v", r.Header)
	r = r.WithContext(ctx)
	res, err := c.httpClient.Do(r)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, res.Body); err != nil {
		return errors.Wrap(err, "reading body")
	}
	c.logf("<< %s", buf.String())
	bodyStr := buf.String()

	if err := json.NewDecoder(&buf).Decode(&gr); err != nil {
		if res.StatusCode != http.StatusOK {
			return errors.Errorf("graphql: server returned a non-200 status code: %v", res.StatusCode)
		}
		return errors.Wrap(err, "decoding response")
	}
	if len(gr.Errors) > 0 {
		return gr.Errors
	}

	// Handle the http status codes before handling response from the graphql endpoint.
	// If this is not done then !200 status codes will just be ignored without the caller even noticing, instead
	// the caller just gets back an empty result set, suggesting that the query did not found any result.
	// The reason for this is that for example in case of a 404,401,403 etc. the request is rejected before
	// it even hits an graphql handler on the server side.
	// As a result the response returned by this non graphql component is not compliant to the graphql spec.
	if res.StatusCode != http.StatusOK {
		return errors.Errorf("graphql: server returned a non-200 status code: %v - %v", res.StatusCode, bodyStr)
	}
	return nil
}

// WithHTTPClient specifies the underlying http.Client to use when
// making requests.
//
//	NewClient(endpoint, WithHTTPClient(specificHTTPClient))
func WithHTTPClient(httpclient *http.Client) ClientOption {
	return func(client *Client) {
		client.httpClient = httpclient
	}
}

// UseMultipartForm uses multipart/form-data and activates support for
// files.
func UseMultipartForm() ClientOption {
	return func(client *Client) {
		client.useMultipartForm = true
	}
}

// ImmediatelyCloseReqBody will close the req body immediately after each request body is ready
func ImmediatelyCloseReqBody() ClientOption {
	return func(client *Client) {
		client.closeReq = true
	}
}

// ClientOption are functions that are passed into NewClient to
// modify the behaviour of the Client.
type ClientOption func(*Client)

// Errors contains all the errors that were returned by the GraphQL server.
type Errors []Error

func (ee Errors) Error() string {
	if len(ee) == 0 {
		return "no errors"
	}
	errs := make([]string, len(ee))
	for i, e := range ee {
		errs[i] = e.Message
	}
	return "graphql: " + strings.Join(errs, "; ")
}

// An Error contains error information returned by the GraphQL server.
type Error struct {
	// Message contains the error message.
	Message string
	// Locations contains the locations in the GraphQL document that caused the
	// error if the error can be associated to a particular point in the
	// requested GraphQL document.
	Locations []Location
	// Path contains the key path of the response field which experienced the
	// error. This allows clients to identify whether a nil result is
	// intentional or caused by a runtime error.
	Path []interface{}
	// Extensions may contain additional fields set by the GraphQL service,
	// such as	an error code.
	Extensions map[string]interface{}
}

// A Location is a location in the GraphQL query that resulted in an error.
// The location may be returned as part of an error response.
type Location struct {
	Line   int
	Column int
}

func (e Error) Error() string {
	return "graphql: " + e.Message
}

type graphResponse struct {
	Data   interface{}
	Errors Errors
}

// Request is a GraphQL request.
type Request struct {
	q     string
	vars  map[string]interface{}
	files []File

	// Header represent any request headers that will be set
	// when the request is made.
	Header http.Header
}

// NewRequest makes a new Request with the specified string.
func NewRequest(q string) *Request {
	req := &Request{
		q:      q,
		Header: make(map[string][]string),
	}
	return req
}

// Var sets a variable.
func (req *Request) Var(key string, value interface{}) {
	if req.vars == nil {
		req.vars = make(map[string]interface{})
	}
	req.vars[key] = value
}

// Vars gets the variables for this Request.
func (req *Request) Vars() map[string]interface{} {
	return req.vars
}

// Files gets the files in this request.
func (req *Request) Files() []File {
	return req.files
}

// Query gets the query string of this request.
func (req *Request) Query() string {
	return req.q
}

// File sets a file to upload.
// Files are only supported with a Client that was created with
// the UseMultipartForm option.
func (req *Request) File(fieldname, filename string, r io.Reader) {
	req.files = append(req.files, File{
		Field: fieldname,
		Name:  filename,
		R:     r,
	})
}

// File represents a file to upload.
type File struct {
	Field string
	Name  string
	R     io.Reader
}
