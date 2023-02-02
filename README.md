# graphql [![GoDoc](https://godoc.org/github.com/setnicka/graphql?status.png)](http://godoc.org/github.com/setnicka/graphql) [![Go Report Card](https://goreportcard.com/badge/github.com/setnicka/graphql)](https://goreportcard.com/report/github.com/setnicka/graphql)

This is a **fork of the original** (and unmaintained) **[machinebox/graphql](https://github.com/machinebox/graphql)**
project with some additional added pull requests.

Feel free to send another pull requests and use this copy of the repository. But
warning: I will try to maintain this fork, but this is a very low priority task.

---

Low-level GraphQL client for Go.

* Simple, familiar API
* Respects `context.Context` timeouts and cancellation
* Build and execute any kind of GraphQL request
* Use strong Go types for response data
* Use variables and upload files
* Simple error handling

## Installation
Make sure you have a working Go environment. To install graphql, simply run:

```
$ go get github.com/setnicka/graphql
```

## Usage

```go
import "context"

// create a client (safe to share across requests)
client := graphql.NewClient("https://setnicka.io/graphql")

// make a request
req := graphql.NewRequest(`
    query ($key: String!) {
        items (id:$key) {
            field1
            field2
            field3
        }
    }
`)

// set any variables
req.Var("key", "value")

// set header fields
req.Header.Set("Cache-Control", "no-cache")

// define a Context for the request
ctx := context.Background()

// run it and capture the response
var respData ResponseStruct
if err := client.Run(ctx, req, &respData); err != nil {
    log.Fatal(err)
}
```

### File support via multipart form data

By default, the package will send a JSON body. To enable the sending of files, you can opt to
use multipart form data instead using the `UseMultipartForm` option when you create your `Client`:

```
client := graphql.NewClient("https://machinebox.io/graphql", graphql.UseMultipartForm())
```

For more information, [read the godoc package documentation](http://godoc.org/github.com/setnicka/graphql) or the [blog post](https://blog.machinebox.io/a-graphql-client-library-for-go-5bffd0455878).

## Thanks

Thanks to [Chris Broadfoot](https://github.com/broady) for design help.
