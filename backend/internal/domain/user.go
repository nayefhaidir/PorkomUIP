package domain

type User struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	Role         string   `json:"role"`
	JobTitle     *string  `json:"jobTitle"`
	DepartmentID *string  `json:"departmentId"`
	PageAccess   []string `json:"pageAccess"`
	Active       bool     `json:"isActive"`
	Department   any      `json:"department,omitempty"`
}
