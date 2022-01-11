package main

import (
	"database/sql"
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

func (d *Database) Close() {
	d.db.Close()
}
