package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-msgauth/dkim"
)

type DeactObject struct {
	DeactVersion int    `json:"deact_version,omitempty"`
	Public       bool   `json:"public,omitempty"`
	Actor        string `json:"actor,omitempty"`
	Action       string `json:"action,omitempty"`
	Target       string `json:"target,omitempty"`
	Content      string `json:"content,omitempty"`
	Email        string `json:"email,omitempty"`
}

type EntriesQuery struct {
	Public  *bool
	Actor   *string
	Action  *string
	Target  *string
	Content bool
	Email   bool
}

func providers() []string {
	return []string{"fastmail", "gmail"}
}

func main() {

	providerStr := "IMAP Provider. Currently support"
	for _, provider := range providers() {
		providerStr += ", " + provider
	}
	providerStr += "."

	provider := flag.String("provider", "", providerStr)
	username := flag.String("username", "", "IMAP Username")
	password := flag.String("password", "", "IMAP password")
	lastUidArg := flag.Int("last-uid", 0, "Last UID")
	deactFolderName := flag.String("deact-folder-name", "deact", "Deact folder name on server")
	pollingInterval := flag.Int("polling-interval", 4, "Polling interval in seconds")
	flag.Parse()

	if *username == "" {
		log.Fatal("Missing username")
	}

	if *password == "" {
		log.Fatal("Missing password")
	}

	var imapServer string
	switch *provider {
	case "fastmail":
		imapServer = "imap.fastmail.com:993"
	case "gmail":
		imapServer = "imap.gmail.com:993"
	default:
		log.Fatal("Invalid provider")
	}

	db := NewDatabase()
	defer db.Close()

	apiServer := NewApiServer(db)

	http.Handle("/api/", http.StripPrefix("/api", apiServer))
	go func() {
		http.ListenAndServe(":9004", nil)
	}()

	log.Println("Connecting to server...")

	c, err := client.DialTLS(imapServer, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Connected")

	defer c.Logout()

	if err := c.Login(*username, *password); err != nil {
		log.Fatal(err)
	}
	log.Println("Logged in")

	mbox, err := c.Select("INBOX", false)
	if err != nil {
		log.Fatal(err)
	}

	var lastUid uint32

	if *lastUidArg != 0 {
		lastUid = uint32(*lastUidArg)
		err = db.SetLastUid(lastUid)
		if err != nil {
			log.Fatal(err)
		}
	}

	lastUid, err = db.GetLastUid()
	if err != nil {
		log.Fatal(err)
	}

	getMessages := func(c *client.Client, mbox *imap.MailboxStatus) []uint32 {

		var moveList []uint32

		seqset := new(imap.SeqSet)
		seqset.AddRange(lastUid+1, 0)

		section := &imap.BodySectionName{}
		items := []imap.FetchItem{imap.FetchUid, section.FetchItem()}

		messages := make(chan *imap.Message, 10)
		done := make(chan error, 1)
		go func() {
			done <- c.UidFetch(seqset, items, messages)
		}()

		var newLastUid uint32
		for msg := range messages {
			newLastUid = msg.Uid

			fmt.Println("Message", msg.Uid)
			body := msg.GetBody(section)
			if body == nil {
				log.Println("WARNING: Server didn't returned message body")
				continue
			}

			bodyBytes, err := io.ReadAll(body)
			if err != nil {
				log.Println(err)
				continue
			}

			parsedBody, err := mail.ReadMessage(bytes.NewReader(bodyBytes))
			if err != nil {
				log.Println(err)
				continue
			}

			subject := parsedBody.Header.Get("Subject")

			if !strings.HasPrefix(subject, "deact-version:1") {
				log.Println("Invalid deact-version. Skipping")
				continue
			}

			verifications, err := dkim.Verify(bytes.NewReader(bodyBytes))
			if err != nil {
				log.Println(err)
				continue
			}

			if len(verifications) == 0 {
				log.Println("WARNING: No DKIM found")
			}

			//printJson(parsedBody.Header)
			//fmt.Println(string(bodyBytes))

			for _, v := range verifications {
				if v.Err == nil {
					log.Println("Valid signature for:", v.Domain)
					//for _, header := range v.HeaderKeys {
					//        value := parsedBody.Header.Get(header)
					//        fmt.Printf("%s: %s\n", header, value)
					//}

					address, err := mail.ParseAddress(parsedBody.Header.Get("From"))
					if err != nil {
						log.Println(err)
						continue
					}

					addrParts := strings.Split(address.Address, "@")
					domain := addrParts[1]

					if domain != v.Domain {
						log.Println("Domain doesn't match sender. Skipping")
						continue
					}

					deactObj, err := parseDeactText(subject)
					if err != nil {
						log.Println(err)
						continue
					}

					deactObj.Actor = address.Address

					err = db.InsertEntry(deactObj, string(bodyBytes))
					if err != nil {
						log.Println(err)
						continue
					}

					switch deactObj.Action {
					case "upvote":
					case "follow":
					default:
						log.Println("Invalid deact action " + deactObj.Action)
						continue
					}

					printJson(deactObj)

					moveList = append(moveList, msg.Uid)

				} else {
					log.Println("Invalid signature for:", v.Domain, v.Err)
				}
			}
		}

		if err := <-done; err != nil {
			log.Println(err)
			return moveList
		}

		lastUid = newLastUid
		err = db.SetLastUid(newLastUid)
		if err != nil {
			log.Println(err)
			return moveList
		}

		return moveList
	}

	for {
		fmt.Println("Polling for messages")

		moveList := getMessages(c, mbox)
		fmt.Println(moveList)

		if *deactFolderName != "INBOX" {
			moveAll(c, moveList, *deactFolderName)
		}

		time.Sleep(time.Duration(*pollingInterval) * time.Second)
	}
}

func moveAll(c *client.Client, moveList []uint32, deactFolderName string) {
	for _, uid := range moveList {
		seqset := new(imap.SeqSet)
		seqset.AddNum(uid)

		fmt.Println(deactFolderName)
		err := c.UidMove(seqset, deactFolderName)
		if err != nil {
			log.Println(err)
			continue
		}
	}
}

func parseDeactText(text string) (*DeactObject, error) {
	parts := strings.Split(text, ",")

	obj := &DeactObject{}

	for _, part := range parts {
		attrParts := strings.Split(part, ":")
		key := strings.TrimSpace(attrParts[0])
		value := strings.TrimSpace(attrParts[1])

		switch key {
		case "deact-version":
			var err error
			obj.DeactVersion, err = strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
		case "public":
			if value == "true" {
				obj.Public = true
			} else {
				obj.Public = false
			}
		//case "actor":
		//        obj.Actor = value
		case "action":
			obj.Action = value
		case "target":
			obj.Target = value
		//case "content":
		//        obj.Content = value
		default:
			return nil, errors.New("Invalid deact key: " + key)
		}
	}

	return obj, nil
}

func printJson(data interface{}) {
	d, _ := json.MarshalIndent(data, "", "  ")
	fmt.Println(string(d))
}
