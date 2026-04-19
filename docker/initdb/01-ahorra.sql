-- Script de inicialización para supabase/postgres.
-- Crea el rol y la base de datos que usa la app (la imagen no respeta
-- POSTGRES_USER / POSTGRES_DB como la imagen oficial de postgres).
-- Este archivo se ejecuta solo en la primera inicialización del volumen.

CREATE ROLE ahorra WITH LOGIN PASSWORD 'ahorra_dev_pw';
CREATE DATABASE ahorra OWNER ahorra;
GRANT ALL PRIVILEGES ON DATABASE ahorra TO ahorra;
