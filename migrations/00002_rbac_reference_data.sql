-- +goose Up
INSERT INTO roles (id,code,name) VALUES
('018f0000-0000-7000-8000-000000000001','super_admin','Super administrator'),
('018f0000-0000-7000-8000-000000000002','organization_admin','Organization administrator'),
('018f0000-0000-7000-8000-000000000003','instructor','Instructor'),
('018f0000-0000-7000-8000-000000000004','student','Student')
ON CONFLICT(code) DO NOTHING;

INSERT INTO permissions (id,code,description) VALUES
('018f0000-0000-7001-8000-000000000001','platform.manage','Manage platform'),
('018f0000-0000-7001-8000-000000000002','organizations.manage','Manage organizations'),
('018f0000-0000-7001-8000-000000000003','users.manage','Manage users'),
('018f0000-0000-7001-8000-000000000004','courses.manage','Manage courses'),
('018f0000-0000-7001-8000-000000000005','assessments.manage','Manage assessments'),
('018f0000-0000-7001-8000-000000000006','submissions.grade','Grade submissions'),
('018f0000-0000-7001-8000-000000000007','courses.learn','Learn in courses'),
('018f0000-0000-7001-8000-000000000008','reports.view','View reports'),
('018f0000-0000-7001-8000-000000000009','payments.view','View payments'),
('018f0000-0000-7001-8000-000000000010','audit.view','View audit logs')
ON CONFLICT(code) DO NOTHING;

INSERT INTO role_permissions (role_id,permission_id)
SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.code='super_admin'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id,permission_id)
SELECT r.id,p.id FROM roles r JOIN permissions p ON p.code IN ('users.manage','courses.manage','assessments.manage','submissions.grade','reports.view','payments.view','audit.view') WHERE r.code='organization_admin'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id,permission_id)
SELECT r.id,p.id FROM roles r JOIN permissions p ON p.code IN ('courses.manage','assessments.manage','submissions.grade','reports.view') WHERE r.code='instructor'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id,permission_id)
SELECT r.id,p.id FROM roles r JOIN permissions p ON p.code='courses.learn' WHERE r.code='student'
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM role_permissions WHERE role_id IN (SELECT id FROM roles WHERE code IN ('super_admin','organization_admin','instructor','student'));
DELETE FROM permissions WHERE code IN ('platform.manage','organizations.manage','users.manage','courses.manage','assessments.manage','submissions.grade','courses.learn','reports.view','payments.view','audit.view');
DELETE FROM roles WHERE code IN ('super_admin','organization_admin','instructor','student');
