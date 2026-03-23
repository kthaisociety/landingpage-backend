package models

type TeamProjectPair struct {
	TeamId    uint `gorm:"primaryKey" json:"-"`
	ProjectId uint `gorm:"primaryKey" json:"-"`
}
