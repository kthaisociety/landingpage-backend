package models

type TeamMemberPair struct {
	TeamId uint `gorm:"primaryKey" json:"-"`
	// UserId uint `gorm:"primaryKey" json:"-"`
	TeamMemberId uint `gorm:"primaryKey" json:"-"`
}
