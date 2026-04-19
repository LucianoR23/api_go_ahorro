-- Orden inverso al up: primero lo que referencia, después lo referenciado.
DROP INDEX IF EXISTS idx_payment_methods_owner;
DROP INDEX IF EXISTS idx_banks_owner;

DROP TABLE IF EXISTS credit_cards;
DROP TABLE IF EXISTS payment_methods;
DROP TABLE IF EXISTS banks;
