package main

import (
	"log"
)

func errorLog(v ...interface{}) {
	log.Println(v)
}

func debugLog(v ...interface{}) {
	log.Println(v)
}
