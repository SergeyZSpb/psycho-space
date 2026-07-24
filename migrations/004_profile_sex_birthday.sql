-- 004_profile_sex_birthday: store the remaining fields VK's base right
-- (vkid.personal_info) transmits — sex and date of birth — encrypted at rest,
-- like the other profile columns. Nullable: pre-existing rows and logins where
-- VK omits the field stay NULL.
ALTER TABLE accounts
    ADD COLUMN sex_enc      bytea,
    ADD COLUMN birthday_enc bytea;
