package models

type TeamUserPair struct {
	TeamId uint `gorm:"primaryKey" json:"-"`
	UserId uint `gorm:"primaryKey" json:"-"`
}
