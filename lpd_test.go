package libtorrent

import (
	"bufio"
	"bytes"
	"log"
	"net/http"
	"testing"
)

func TestMultiInfohash(t *testing.T) {
	html := `BT-SEARCH * HTTP/1.1
Host: 239.192.152.143:6771
Port: 3333
Infohash: 123123
Infohash: 2222
cookie: name=value


`
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader([]byte(html))))
	if err != nil {
		log.Println("receiver", err)
		return
	}
	var ihs []string = req.Header[http.CanonicalHeaderKey("Infohash")]
	if ihs == nil {
		log.Println("receiver", "No Infohash")
		return
	}
	for _, ih := range ihs {
		log.Println(ih)
	}
}
