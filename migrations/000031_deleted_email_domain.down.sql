UPDATE users
SET email = replace(email, '@deleted.invalid', '@ahorra.deleted')
WHERE email LIKE 'deleted+%@deleted.invalid';
