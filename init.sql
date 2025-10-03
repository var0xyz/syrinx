-- Create the database if it doesn't exist (though it should already exist from POSTGRES_DB)
-- This is just a safety measure
SELECT 'CREATE DATABASE syrinx'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'syrinx')\gexec

-- Connect to the syrinx database
\c syrinx;

-- Install pg_uuidv7 extension for UUID v7 generation
-- This extension provides uuid_generate_v7() function
CREATE EXTENSION IF NOT EXISTS pg_uuidv7;
