package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dio_wapp/server/internal/model"
	"dio_wapp/server/internal/wechat"
	"dio_wapp/server/internal/xerr"

	"gorm.io/gorm"
)

type AuthService struct {
	db                *gorm.DB
	wechatClient      *wechat.Client
	jwtManager        *JWTManager
	adminOpenIDs      map[string]struct{}
	staffOpenIDs      map[string]struct{}
	teamLeaderOpenIDs map[string]struct{}
	defaultAgencyName string
}

type StaffGroupDTO struct {
	AgencyID    uint64 `json:"agency_id"`
	AgencyName  string `json:"agency_name"`
	GroupID     uint64 `json:"group_id"`
	GroupName   string `json:"group_name"`
	DisplayName string `json:"display_name"`
	MemberRole  string `json:"member_role"`
}

type AdminAgencyDTO struct {
	AgencyID    uint64 `json:"agency_id"`
	AgencyName  string `json:"agency_name"`
	DisplayName string `json:"display_name"`
}

type UserDTO struct {
	ID            uint64           `json:"id"`
	OpenID        string           `json:"openid"`
	Nickname      string           `json:"nickname"`
	Avatar        string           `json:"avatar"`
	Status        string           `json:"status"`
	PointsBalance int64            `json:"points_balance"`
	PointAccounts []AgencyPointDTO `json:"point_accounts"`
	Roles         []string         `json:"roles"`
	StaffGroups   []StaffGroupDTO  `json:"staff_groups"`
	AdminAgencies []AdminAgencyDTO `json:"admin_agencies"`
}

type identityDTO struct {
	Roles         []string
	StaffGroups   []StaffGroupDTO
	AdminAgencies []AdminAgencyDTO
	PointAccounts []AgencyPointDTO
}

type LoginResult struct {
	Token     string  `json:"token"`
	ExpiresIn int64   `json:"expires_in"`
	User      UserDTO `json:"user"`
}

func NewAuthService(db *gorm.DB, wechatClient *wechat.Client, jwtManager *JWTManager, adminOpenIDs map[string]struct{}, defaultAgencyName string, memberOpenIDs ...map[string]struct{}) *AuthService {
	defaultAgencyName = strings.TrimSpace(defaultAgencyName)
	if defaultAgencyName == "" {
		defaultAgencyName = "默认事务所"
	}
	configuredStaffOpenIDs := map[string]struct{}{}
	if len(memberOpenIDs) > 0 && memberOpenIDs[0] != nil {
		configuredStaffOpenIDs = memberOpenIDs[0]
	}
	configuredTeamLeaderOpenIDs := map[string]struct{}{}
	if len(memberOpenIDs) > 1 && memberOpenIDs[1] != nil {
		configuredTeamLeaderOpenIDs = memberOpenIDs[1]
	}

	return &AuthService{
		db:                db,
		wechatClient:      wechatClient,
		jwtManager:        jwtManager,
		adminOpenIDs:      adminOpenIDs,
		staffOpenIDs:      configuredStaffOpenIDs,
		teamLeaderOpenIDs: configuredTeamLeaderOpenIDs,
		defaultAgencyName: defaultAgencyName,
	}
}

func (s *AuthService) Login(ctx context.Context, code string) (LoginResult, error) {
	session, err := s.wechatClient.Code2Session(ctx, code)
	if err != nil {
		return LoginResult{}, err
	}

	var user model.User
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		err := tx.Where("open_id = ?", session.OpenID).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			user = model.User{
				OpenID:        session.OpenID,
				UnionID:       session.UnionID,
				Status:        model.UserStatusNormal,
				PointsBalance: 0,
				LastLoginAt:   &now,
			}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			updates := map[string]any{"last_login_at": &now}
			if session.UnionID != "" && user.UnionID == "" {
				updates["union_id"] = session.UnionID
			}
			if err := tx.Model(&user).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.First(&user, user.ID).Error; err != nil {
				return err
			}
		}

		if user.Status == model.UserStatusDisabled {
			return xerr.New(403, "user_disabled", "user is disabled")
		}

		if _, ok := s.adminOpenIDs[user.OpenID]; ok {
			agency, err := ensureAgency(tx, s.defaultAgencyName)
			if err != nil {
				return err
			}
			if err := ensureAdminMember(tx, user.ID, agency.ID); err != nil {
				return err
			}
		}
		_, configuredTeamLeader := s.teamLeaderOpenIDs[user.OpenID]
		if _, ok := s.staffOpenIDs[user.OpenID]; ok && !configuredTeamLeader {
			agency, err := ensureAgency(tx, s.defaultAgencyName)
			if err != nil {
				return err
			}
			group, err := ensureIdolGroup(tx, agency.ID, defaultShopGroupName)
			if err != nil {
				return err
			}
			if err := ensureStaffMember(tx, user.ID, agency.ID, group.ID, model.StaffRoleStaff); err != nil {
				return err
			}
		}
		if configuredTeamLeader {
			agency, err := ensureAgency(tx, s.defaultAgencyName)
			if err != nil {
				return err
			}
			group, err := ensureIdolGroup(tx, agency.ID, defaultShopGroupName)
			if err != nil {
				return err
			}
			if err := ensureStaffMember(tx, user.ID, agency.ID, group.ID, model.StaffRoleTeamLeader); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return LoginResult{}, err
	}

	identity, err := s.Identity(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}

	token, err := s.jwtManager.Sign(user.ID)
	if err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Token:     token,
		ExpiresIn: int64(s.jwtManager.TTL().Seconds()),
		User:      toUserDTO(user, identity),
	}, nil
}

func (s *AuthService) Me(ctx context.Context, userID uint64) (UserDTO, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return UserDTO{}, xerr.New(401, "invalid_token", "invalid token")
		}
		return UserDTO{}, err
	}
	if user.Status == model.UserStatusDisabled {
		return UserDTO{}, xerr.New(403, "user_disabled", "user is disabled")
	}

	identity, err := s.Identity(ctx, user.ID)
	if err != nil {
		return UserDTO{}, err
	}

	return toUserDTO(user, identity), nil
}

func (s *AuthService) EnsureActiveUser(ctx context.Context, userID uint64) error {
	var user model.User
	if err := s.db.WithContext(ctx).Select("id", "status").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return xerr.New(401, "invalid_token", "invalid token")
		}
		return err
	}
	if user.Status != model.UserStatusNormal {
		return xerr.New(403, "user_disabled", "user is disabled")
	}
	return nil
}

func (s *AuthService) Identity(ctx context.Context, userID uint64) (identityDTO, error) {
	identity := identityDTO{Roles: []string{"user"}}
	pointAccounts, err := listAgencyPointAccounts(ctx, s.db, userID)
	if err != nil {
		return identityDTO{}, err
	}
	identity.PointAccounts = pointAccounts

	staffGroups, err := s.staffGroups(ctx, userID)
	if err != nil {
		return identityDTO{}, err
	}
	if len(staffGroups) > 0 {
		identity.Roles = append(identity.Roles, "staff")
		identity.StaffGroups = staffGroups
		for _, group := range staffGroups {
			if group.MemberRole == model.StaffRoleTeamLeader {
				identity.Roles = append(identity.Roles, "team_leader")
				break
			}
		}
	}

	adminAgencies, err := s.adminAgencies(ctx, userID)
	if err != nil {
		return identityDTO{}, err
	}
	if len(adminAgencies) > 0 {
		identity.Roles = append(identity.Roles, "admin")
		identity.AdminAgencies = adminAgencies
	}

	return identity, nil
}

func (s *AuthService) HasStaffGroup(ctx context.Context, userID uint64, agencyID uint64, groupID uint64) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Table("staff_members").
		Joins("JOIN users ON users.id = staff_members.user_id").
		Joins("JOIN agencies ON agencies.id = staff_members.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = staff_members.group_id AND idol_groups.agency_id = staff_members.agency_id").
		Where("staff_members.user_id = ?", userID).
		Where("staff_members.agency_id = ? AND staff_members.group_id = ?", agencyID, groupID).
		Where("staff_members.status = ?", model.MemberStatusNormal).
		Where("users.status = ?", model.UserStatusNormal).
		Where("agencies.status = ? AND idol_groups.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *AuthService) HasTeamLeaderGroup(ctx context.Context, userID uint64, agencyID uint64, groupID uint64) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Table("staff_members").
		Joins("JOIN users ON users.id = staff_members.user_id").
		Joins("JOIN agencies ON agencies.id = staff_members.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = staff_members.group_id AND idol_groups.agency_id = staff_members.agency_id").
		Where("staff_members.user_id = ?", userID).
		Where("staff_members.agency_id = ? AND staff_members.group_id = ?", agencyID, groupID).
		Where("staff_members.role = ? AND staff_members.status = ?", model.StaffRoleTeamLeader, model.MemberStatusNormal).
		Where("users.status = ?", model.UserStatusNormal).
		Where("agencies.status = ? AND idol_groups.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *AuthService) HasAdminAgency(ctx context.Context, userID uint64, agencyID uint64) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).
		Table("admin_members").
		Joins("JOIN users ON users.id = admin_members.user_id").
		Joins("JOIN agencies ON agencies.id = admin_members.agency_id").
		Where("admin_members.user_id = ? AND admin_members.agency_id = ?", userID, agencyID).
		Where("admin_members.status = ? AND agencies.status = ? AND users.status = ?", model.MemberStatusNormal, model.MemberStatusNormal, model.UserStatusNormal).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *AuthService) staffGroups(ctx context.Context, userID uint64) ([]StaffGroupDTO, error) {
	var rows []struct {
		AgencyID    uint64 `gorm:"column:agency_id"`
		AgencyName  string `gorm:"column:agency_name"`
		GroupID     uint64 `gorm:"column:group_id"`
		GroupName   string `gorm:"column:group_name"`
		DisplayName string `gorm:"column:display_name"`
		MemberRole  string `gorm:"column:member_role"`
	}

	err := s.db.WithContext(ctx).
		Table("staff_members").
		Select("staff_members.agency_id, agencies.name AS agency_name, staff_members.group_id, idol_groups.name AS group_name, staff_members.display_name, staff_members.role AS member_role").
		Joins("JOIN agencies ON agencies.id = staff_members.agency_id").
		Joins("JOIN idol_groups ON idol_groups.id = staff_members.group_id AND idol_groups.agency_id = staff_members.agency_id").
		Where("staff_members.user_id = ?", userID).
		Where("staff_members.status = ?", model.MemberStatusNormal).
		Where("agencies.status = ? AND idol_groups.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
		Order("agencies.id ASC, idol_groups.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make([]StaffGroupDTO, 0, len(rows))
	for _, row := range rows {
		result = append(result, StaffGroupDTO{
			AgencyID:    row.AgencyID,
			AgencyName:  row.AgencyName,
			GroupID:     row.GroupID,
			GroupName:   row.GroupName,
			DisplayName: row.DisplayName,
			MemberRole:  normalizedStaffRole(row.MemberRole),
		})
	}
	return result, nil
}

func (s *AuthService) adminAgencies(ctx context.Context, userID uint64) ([]AdminAgencyDTO, error) {
	var rows []struct {
		AgencyID    uint64 `gorm:"column:agency_id"`
		AgencyName  string `gorm:"column:agency_name"`
		DisplayName string `gorm:"column:display_name"`
	}

	err := s.db.WithContext(ctx).
		Table("admin_members").
		Select("admin_members.agency_id, agencies.name AS agency_name, admin_members.display_name").
		Joins("JOIN agencies ON agencies.id = admin_members.agency_id").
		Where("admin_members.user_id = ?", userID).
		Where("admin_members.status = ? AND agencies.status = ?", model.MemberStatusNormal, model.MemberStatusNormal).
		Order("agencies.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make([]AdminAgencyDTO, 0, len(rows))
	for _, row := range rows {
		result = append(result, AdminAgencyDTO{
			AgencyID:    row.AgencyID,
			AgencyName:  row.AgencyName,
			DisplayName: row.DisplayName,
		})
	}
	return result, nil
}

func ensureAgency(tx *gorm.DB, name string) (model.Agency, error) {
	var agencies []model.Agency
	if err := tx.Order("id ASC").Limit(2).Find(&agencies).Error; err != nil {
		return model.Agency{}, err
	}
	if len(agencies) > 1 {
		return model.Agency{}, fmt.Errorf("single-agency invariant violated: found more than one agency")
	}
	if len(agencies) == 1 {
		return agencies[0], nil
	}

	agency := model.Agency{
		Name:   name,
		Status: model.MemberStatusNormal,
		Remark: "created from ADMIN_OPENIDS",
	}
	if err := tx.Create(&agency).Error; err != nil {
		return model.Agency{}, err
	}
	return agency, nil
}

func EnsureSingleAgency(ctx context.Context, db *gorm.DB, name string) (model.Agency, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "默认事务所"
	}
	var agency model.Agency
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		agency, err = ensureAgency(tx, name)
		return err
	})
	return agency, err
}

func ensureAdminMember(tx *gorm.DB, userID uint64, agencyID uint64) error {
	var admin model.AdminMember
	err := tx.Where("user_id = ? AND agency_id = ?", userID, agencyID).First(&admin).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	return tx.Create(&model.AdminMember{
		AgencyID:    agencyID,
		UserID:      userID,
		Status:      model.MemberStatusNormal,
		DisplayName: "事务所管理员",
		Remark:      "created from ADMIN_OPENIDS",
	}).Error
}

func ensureStaffMember(tx *gorm.DB, userID uint64, agencyID uint64, groupID uint64, role string) error {
	role = normalizedStaffRole(role)
	var staff model.StaffMember
	err := tx.Where("user_id = ? AND agency_id = ? AND group_id = ?", userID, agencyID, groupID).First(&staff).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	displayName := "现场 Staff"
	remark := "created from STAFF_OPENIDS"
	if role == model.StaffRoleTeamLeader {
		displayName = "团队负责人"
		remark = "created from TEAM_LEADER_OPENIDS"
	}
	return tx.Create(&model.StaffMember{
		AgencyID:    agencyID,
		GroupID:     groupID,
		UserID:      userID,
		Role:        role,
		Status:      model.MemberStatusNormal,
		DisplayName: displayName,
		Remark:      remark,
	}).Error
}

func normalizedStaffRole(role string) string {
	if role == model.StaffRoleTeamLeader {
		return model.StaffRoleTeamLeader
	}
	return model.StaffRoleStaff
}

func toUserDTO(user model.User, identity identityDTO) UserDTO {
	var totalPoints int64
	for _, account := range identity.PointAccounts {
		totalPoints += account.Balance
	}
	return UserDTO{
		ID:            user.ID,
		OpenID:        user.OpenID,
		Nickname:      user.Nickname,
		Avatar:        user.Avatar,
		Status:        user.Status,
		PointsBalance: totalPoints,
		PointAccounts: identity.PointAccounts,
		Roles:         identity.Roles,
		StaffGroups:   identity.StaffGroups,
		AdminAgencies: identity.AdminAgencies,
	}
}
