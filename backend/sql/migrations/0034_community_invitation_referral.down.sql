BEGIN;

-- DEV ROLLBACK ONLY: 按 tenant-scoped 复合外键依赖反向删除 F-COMM-001 Phase 1 表。
DROP TABLE IF EXISTS tier_progress;
DROP TABLE IF EXISTS referral_rewards;
DROP TABLE IF EXISTS referrals;
DROP TABLE IF EXISTS invitations;

COMMIT;
