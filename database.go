package main

import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"log"
)

type Database struct {
	db *sql.DB
}

func NewDatabase() *Database {

	db, err := sql.Open("sqlite3", "./deact.db")
	if err != nil {
		log.Fatal(err)
	}

	sqlStmt := `
	create table if not exists state (id integer not null primary key, last_uid integer);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Printf("%q: %s\n", err, sqlStmt)
		return nil
	}

	sqlStmt = `
	CREATE TABLE IF NOT EXISTS
        deactions(id INTEGER NOT NULL PRIMARY KEY, public INTEGER, actor TEXT, action TEXT, target TEXT, content TEXT, email TEXT);
	`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		log.Printf("%q: %s\n", err, sqlStmt)
		return nil
	}

	return &Database{
		db: db,
	}
}

func (d *Database) GetLastUid() (uint32, error) {
	stmt := `
        SELECT last_uid FROM state
        WHERE id=1
        `
	row := d.db.QueryRow(stmt)

	var lastUid uint32
	err := row.Scan(&lastUid)
	if err != nil {
		return 0, err
	}

	return lastUid, nil
}

func (d *Database) SetLastUid(uid uint32) error {

	stmt := `
        INSERT INTO state(id, last_uid) VALUES(1, ?)
        ON CONFLICT(id) DO UPDATE SET last_uid=?
        `
	_, err := d.db.Exec(stmt, uid, uid)
	if err != nil {
		log.Fatal(err)
	}

	return nil
}

func (d *Database) InsertFollow(obj *DeactObject, emailText string) error {
	stmt := `
        INSERT INTO deactions(public, actor, action, target, content, email) VALUES(?, ?, ?, ?, ?, ?)
        `
	_, err := d.db.Exec(stmt, obj.Public, obj.Actor, obj.Action, obj.Target, obj.Content, emailText)
	if err != nil {
		return err
	}
	return nil
}

func (d *Database) GetEntries(query EntriesQuery) ([]*DeactObject, error) {

	selectStr := "public,actor,action,target"
	// Return all entries by default
	whereStr := "1=1"

	if query.Public != nil {
		whereStr += fmt.Sprintf(" AND public=%t", *query.Public)
	}
	if query.Actor != nil {
		whereStr += fmt.Sprintf(" AND actor='%s'", *query.Actor)
	}
	if query.Action != nil {
		whereStr += fmt.Sprintf(" AND action='%s'", *query.Action)
	}
	if query.Target != nil {
		whereStr += fmt.Sprintf(" AND target='%s'", *query.Target)
	}
	if query.Content {
		selectStr += ",content"
	}
	if query.Email {
		selectStr += ",email"
	}

	stmt := fmt.Sprintf("SELECT %s from deactions WHERE %s;", selectStr, whereStr)

	log.Println(stmt)

	rows, err := d.db.Query(stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []*DeactObject

	for rows.Next() {

		obj := &DeactObject{}

		// TODO: This feels super messy
		if query.Content && query.Email {
			if err := rows.Scan(&obj.Public, &obj.Actor, &obj.Action, &obj.Target, &obj.Content, &obj.Email); err != nil {
				return nil, err
			}
		} else if query.Content {
			if err := rows.Scan(&obj.Public, &obj.Actor, &obj.Action, &obj.Target, &obj.Content); err != nil {
				return nil, err
			}
		} else if query.Email {
			if err := rows.Scan(&obj.Public, &obj.Actor, &obj.Action, &obj.Target, &obj.Email); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&obj.Public, &obj.Actor, &obj.Action, &obj.Target); err != nil {
				return nil, err
			}
		}

		objects = append(objects, obj)
	}

	// If the database is being written to ensure to check for Close
	// errors that may be returned from the driver. The query may
	// encounter an auto-commit error and be forced to rollback changes.
	rerr := rows.Close()
	if rerr != nil {
		return nil, err
	}

	// Rows.Err will report the last error encountered by Rows.Scan.
	if err := rows.Err(); err != nil {
		return nil, err
	}

	//fmt.Printf("%s are %d years old", strings.Join(names, ", "), age)

	return objects, nil
}

func (d *Database) Close() {
	d.db.Close()
}
