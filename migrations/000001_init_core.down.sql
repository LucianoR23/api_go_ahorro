-- Orden inverso al up: primero las tablas que dependen, después las independientes.
-- Las extensiones NO se bajan: son compartidas y borrarlas podría romper otras cosas.
DROP TABLE IF EXISTS household_members;
DROP TABLE IF EXISTS households;
DROP TABLE IF EXISTS users;
