package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/mail"
	"strconv"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-msgauth/dkim"
)

type DeactObject struct {
	DeactVersion int    `json:"deact_version"`
	Public       bool   `json:"public"`
	Actor        string `json:"actor"`
	Action       string `json:"action"`
	Target       string `json:"target"`
	Content      string `json:"content"`
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

	waitForUpdates := func(c *client.Client) []client.Update {

		fmt.Println("waitForUpdates")

		updates := make(chan client.Update, 1)
		c.Updates = updates

		stopped := false
		stop := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- c.Idle(stop, nil)
		}()

		var out []client.Update

		for {
			select {
			case update := <-updates:
				log.Println("New update:", update)
				if !stopped {
					close(stop)
					stopped = true
					out = append(out, update)
				}
			case err := <-done:
				if err != nil {
					log.Fatal(err)
				}
				log.Println("Not idling anymore")
				return out
			}
		}
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

	getMessages := func(c *client.Client, mbox *imap.MailboxStatus) {

		seqset := new(imap.SeqSet)
		seqset.AddRange(lastUid+1, 0)

		section := &imap.BodySectionName{}
		items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, section.FetchItem()}

		messages := make(chan *imap.Message, 10)
		done := make(chan error, 1)
		go func() {
			done <- c.UidFetch(seqset, items, messages)
		}()

		var newLastUid uint32
		for msg := range messages {
			newLastUid = msg.Uid

			//log.Println("* " + msg.Envelope.Subject)
			fmt.Println("Message", msg.Uid, msg.Envelope.Subject)
			body := msg.GetBody(section)
			if body == nil {
				log.Fatal("Server didn't returned message body")
			}

			bodyBytes, err := io.ReadAll(body)
			if err != nil {
				log.Fatal(err)
			}

			parsedBody, err := mail.ReadMessage(bytes.NewReader(bodyBytes))
			if err != nil {
				log.Fatal(err)
			}

			subject := parsedBody.Header.Get("Subject")

			if !strings.HasPrefix(subject, "deact-version:1") {
				fmt.Println("skipping")
				continue
			}

			verifications, err := dkim.Verify(bytes.NewReader(bodyBytes))
			if err != nil {
				fmt.Println("here3")
				log.Fatal(err)
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

					deactObj, err := parseDeactText(subject)
					if err != nil {
						log.Fatal(err)
					}

					address, err := mail.ParseAddress(parsedBody.Header.Get("From"))
					if err != nil {
						log.Fatal(err)
					}

					deactObj.Actor = address.Address

					err = db.InsertFollow(deactObj, string(bodyBytes))
					if err != nil {
						log.Fatal(err)
					}

					switch deactObj.Action {
					case "follow":

					default:
						log.Fatal(errors.New("Invalid deact action " + deactObj.Action))
					}

					printJson(deactObj)
				} else {
					log.Println("Invalid signature for:", v.Domain, v.Err)
				}
			}
		}

		if err := <-done; err != nil {
			log.Fatal(err)
		}

		lastUid = newLastUid
		err = db.SetLastUid(newLastUid)
		if err != nil {
			log.Fatal(err)
		}
	}

	processUpdate := func(c *client.Client, update client.Update) {
		switch u := update.(type) {
		case *client.MailboxUpdate:
			fmt.Println("MailboxUpdate")
			mbox := u.Mailbox
			getMessages(c, mbox)
		case *client.MessageUpdate:
			fmt.Println("MessageUpdate")
		case *client.StatusUpdate:
			fmt.Println("StatusUpdate")
		case *client.ExpungeUpdate:
			fmt.Println("ExpungeUpdate")
		default:
			fmt.Println("Unknown update type")
		}
	}

	getMessages(c, mbox)

	for {
		updates := waitForUpdates(c)

		for _, u := range updates {
			processUpdate(c, u)
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
