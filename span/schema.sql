-- Authorizations table stores all processed authorization transactions
CREATE TABLE Authorizations (
  -- System Trace Audit Number uniquely identifies a transaction
  Stan STRING(12) NOT NULL,
  -- Primary Account Number (card number) - sensitive data
  Pan STRING(19) NOT NULL,
  -- Transaction amount
  Amount FLOAT64 NOT NULL,
  -- Region that processed the transaction
  Region STRING(50) NOT NULL,
  -- Whether the transaction was approved
  Approved BOOL NOT NULL,
  -- Transmission time from the original request
  TransmissionTime TIMESTAMP NOT NULL,
  -- When the record was inserted into Spanner
  InsertedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
) PRIMARY KEY (Stan);

-- Index for querying transactions by region
CREATE INDEX AuthorizationsByRegion
ON Authorizations (Region, TransmissionTime DESC);

-- Index for querying transactions by approval status
CREATE INDEX AuthorizationsByApproval
ON Authorizations (Approved, TransmissionTime DESC);

-- Index for querying transactions by PAN (for cardholder transaction history)
CREATE INDEX AuthorizationsByPan
ON Authorizations (Pan, TransmissionTime DESC); 