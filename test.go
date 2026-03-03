package main

import "strconv"

type User struct {
	ID int
}

type UserDTO struct {
	ID string
}

func convert(users []User) []UserDTO {
	result := make([]UserDTO, 0, len(users))

	for _, u := range users {
		result = append(result, UserDTO{
			ID: strconv.Itoa(u.ID),
		})
	}

	result2 := make([]UserDTO, 0, len(result))
	for _, u := range result {
		result2 = append(result2, UserDTO{
			ID: u.ID,
		})
	}

	return result
}
