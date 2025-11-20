package main

import "github.com/hashicorp/memberlist"

func main() {
	conf := memberlist.DefaultWANConfig()
	l, err := memberlist.Create(conf)
	if err != nil {
		panic(err)
	}
	_, err = l.Join([]string{"127.0.0.1"})
	if err != nil {
		panic(err)
	}

}
