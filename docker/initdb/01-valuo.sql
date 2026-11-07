-- Dev bootstrap for the official postgres image.
-- Creates the least-privilege app role and its database (the superuser
-- 'postgres' is only used for bootstrap).
-- Este archivo se ejecuta solo en la primera inicialización del volumen.

CREATE ROLE valuo WITH LOGIN PASSWORD 'valuo_dev_pw';
CREATE DATABASE valuo OWNER valuo;
GRANT ALL PRIVILEGES ON DATABASE valuo TO valuo;
