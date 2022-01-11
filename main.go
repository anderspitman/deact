package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-msgauth/dkim"
)

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

	lastUid, err := db.GetLastUid()
	if err != nil {
		log.Fatal(err)
	}

	if *lastUidArg != 0 {
		lastUid = uint32(*lastUidArg)
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
			//log.Println("* " + msg.Envelope.Subject)
			fmt.Println("Message", msg.Uid, msg.Envelope.Subject)
			body := msg.GetBody(section)
			if body == nil {
				log.Fatal("Server didn't returned message body")
			}

			newLastUid = msg.Uid

			verifications, err := dkim.Verify(body)
			if err != nil {
				log.Fatal(err)
			}

			if len(verifications) == 0 {
				log.Println("WARNING: No DKIM found")
			}

			for _, v := range verifications {
				if v.Err == nil {
					log.Println("Valid signature for:", v.Domain)
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

func printJson(data interface{}) {
	d, _ := json.MarshalIndent(data, "", "  ")
	fmt.Println(string(d))
}
