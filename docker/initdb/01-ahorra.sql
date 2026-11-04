-- Dev bootstrap for the official postgres image.
-- Creates the least-privilege app role and its database (the superuser
-- 'postgres' is only used for bootstrap).
-- Este archivo se ejecuta solo en la primera inicialización del volumen.

CREATE ROLE ahorra WITH LOGIN PASSWORD 'ahorra_dev_pw';
CREATE DATABASE ahorra OWNER ahorra;
GRANT ALL PRIVILEGES ON DATABASE ahorra TO ahorra;
