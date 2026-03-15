package models

import (
	"github.com/google/uuid"
	"github.com/kestfor/in-memorydb/tests/comparison/utils"
)

type User struct {
	Uuid      string `json:"uuid"`
	Username  string `json:"username"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Address   string `json:"address"`
}

func NewUser() *User {
	id := uuid.New()

	u := User{
		Uuid:      id.String(),
		Username:  utils.RandomString(10),
		FirstName: utils.RandomString(5),
		LastName:  utils.RandomString(10),
		Address:   utils.RandomString(20),
	}

	return &u
}
