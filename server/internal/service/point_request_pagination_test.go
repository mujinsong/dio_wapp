package service

import (
	"context"
	"fmt"
	"testing"

	"dio_wapp/server/internal/model"
)

func TestPointRequestPaginationHistoryAndScope(t *testing.T) {
	f := newPointGrantFixture(t, model.StaffRoleStaff)
	leader := createCorrectionLeader(t, f)
	ctx := context.Background()
	otherGroup := model.IdolGroup{AgencyID: f.agency.ID, Name: "Other team", Status: model.MemberStatusNormal}
	if err := f.service.db.Create(&otherGroup).Error; err != nil {
		t.Fatal(err)
	}
	otherGrant := model.PointGrantRequest{AgencyID: f.agency.ID, GroupID: otherGroup.ID,
		UserID: f.target.ID, SubmittedBy: f.target.ID, Points: 60,
		RequestStatus: model.ApprovalStatusPending, ClientRequestID: "other-team-grant"}
	if err := f.service.db.Create(&otherGrant).Error; err != nil {
		t.Fatal(err)
	}
	otherCorrection := model.PointCorrectionRequest{AgencyID: f.agency.ID, GroupID: otherGroup.ID,
		UserID: f.target.ID, SubmittedBy: f.target.ID, OriginalGrantRequestID: otherGrant.ID,
		Points: 60, RequestStatus: model.ApprovalStatusPending, ClientRequestID: "other-team-correction"}
	if err := f.service.db.Create(&otherCorrection).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		grant := model.PointGrantRequest{AgencyID: f.agency.ID, GroupID: f.group.ID,
			UserID: f.target.ID, SubmittedBy: f.staff.ID, Points: 60,
			RequestStatus: model.ApprovalStatusPending, ClientRequestID: fmt.Sprintf("page-grant-%d", i)}
		if err := f.service.db.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
		correction := model.PointCorrectionRequest{AgencyID: f.agency.ID, GroupID: f.group.ID,
			UserID: f.target.ID, SubmittedBy: f.staff.ID, OriginalGrantRequestID: grant.ID,
			Points: 60, RequestStatus: model.ApprovalStatusPending, ClientRequestID: fmt.Sprintf("page-correction-%d", i)}
		if err := f.service.db.Create(&correction).Error; err != nil {
			t.Fatal(err)
		}
	}

	for _, kind := range []string{"grant", "own", "review"} {
		t.Run(kind, func(t *testing.T) {
			cursor, previous := uint64(0), ^uint64(0)
			count := 0
			for round := 0; round < 10; round++ {
				var ids []uint64
				var next uint64
				var more bool
				if kind == "grant" {
					page, err := f.service.ListPointGrantRequests(ctx, leader.ID, f.group.ID, "pending", cursor, 20)
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range page.Items {
						ids = append(ids, item.ID)
					}
					next, more = page.NextCursor, page.HasMore
				} else {
					var page PointCorrectionRequestPage
					var err error
					if kind == "own" {
						page, err = f.service.ListOwnPointCorrections(ctx, f.staff.ID, "all", cursor, 20)
					} else {
						page, err = f.service.ListReviewPointCorrections(ctx, leader.ID, f.group.ID, "pending", cursor, 20)
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range page.Items {
						ids = append(ids, item.ID)
					}
					next, more = page.NextCursor, page.HasMore
				}
				if len(ids) > 20 {
					t.Fatalf("unbounded page: %d", len(ids))
				}
				for _, id := range ids {
					if id >= previous {
						t.Fatalf("duplicate or out-of-order ID %d after %d", id, previous)
					}
					previous = id
					count++
				}
				if !more {
					if next != 0 {
						t.Fatal("last page has a cursor")
					}
					break
				}
				if next != previous {
					t.Fatal("cursor does not match last item")
				}
				cursor = next
			}
			if count != 105 {
				t.Fatalf("history truncated: got %d records", count)
			}
		})
	}
	page, err := f.service.ListOwnPointCorrections(ctx, leader.ID, "all", 0, 20)
	if err != nil || len(page.Items) != 0 || page.Items == nil || page.HasMore {
		t.Fatalf("own history leaked: %+v %v", page, err)
	}
	_, err = f.service.ListPointGrantRequests(ctx, f.staff.ID, f.group.ID, "all", 0, 20)
	assertCorrectionErrorCode(t, err, "point_grant_review_forbidden")
	_, err = f.service.ListReviewPointCorrections(ctx, f.staff.ID, f.group.ID, "all", 0, 20)
	assertCorrectionErrorCode(t, err, "point_grant_review_forbidden")
	_, err = f.service.ListPointGrantRequests(ctx, leader.ID, otherGroup.ID, "all", 0, 20)
	assertCorrectionErrorCode(t, err, "point_grant_review_forbidden")
	_, err = f.service.ListReviewPointCorrections(ctx, leader.ID, otherGroup.ID, "all", 0, 20)
	assertCorrectionErrorCode(t, err, "point_grant_review_forbidden")
	grants, err := f.service.ListPointGrantRequests(ctx, leader.ID, f.group.ID, "approved", 0, 20)
	if err != nil || len(grants.Items) != 0 {
		t.Fatalf("grant status filter failed: %+v %v", grants, err)
	}
	corrections, err := f.service.ListReviewPointCorrections(ctx, leader.ID, f.group.ID, "approved", 0, 20)
	if err != nil || len(corrections.Items) != 0 {
		t.Fatalf("correction status filter failed: %+v %v", corrections, err)
	}
	grants, err = f.service.ListPointGrantRequests(ctx, leader.ID, f.group.ID, "all", 0, 1000)
	if err != nil || len(grants.Items) != 50 || !grants.HasMore {
		t.Fatalf("grant limit not capped: %+v %v", grants, err)
	}
	corrections, err = f.service.ListOwnPointCorrections(ctx, f.staff.ID, "all", 0, 1000)
	if err != nil || len(corrections.Items) != 50 || !corrections.HasMore {
		t.Fatalf("correction limit not capped: %+v %v", corrections, err)
	}
}
