CREATE TABLE monitoring_policies (
 id integer PRIMARY KEY CHECK (id = 1),
 policy_json jsonb NOT NULL,
 revision bigint NOT NULL DEFAULT 1,
 cleanup_pending boolean NOT NULL DEFAULT false,
 cleanup_task_id text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO monitoring_policies(id, policy_json)
 VALUES (1, '{"enabled":true,"interval_seconds":5,"retention_days":3,"exclusions":[]}');

CREATE TABLE monitoring_instances (
 id varchar(64) PRIMARY KEY,
 node_id text NOT NULL, engine_id text NOT NULL, container_id varchar(64) NOT NULL,
 node_name text NOT NULL, name text NOT NULL, image text NOT NULL,
 project text NOT NULL DEFAULT '', service text NOT NULL DEFAULT '',
 first_seen timestamptz NOT NULL, last_seen timestamptz NOT NULL,
 UNIQUE(node_id, engine_id, container_id)
);
CREATE INDEX monitoring_instances_last_seen ON monitoring_instances(last_seen DESC, id);
CREATE INDEX monitoring_instances_node ON monitoring_instances(node_id, container_id);

CREATE TABLE monitoring_samples (
 instance_id varchar(64) NOT NULL, recorded_at timestamptz NOT NULL,
 sampled_at timestamptz NOT NULL, started_at text NOT NULL, session text NOT NULL,
 interval_seconds integer NOT NULL,
 cpu double precision, memory double precision, memory_limit double precision,
 network_rx double precision, network_tx double precision,
 block_read double precision, block_write double precision, pids double precision,
 counters_json jsonb NOT NULL DEFAULT '{}',
 PRIMARY KEY(instance_id, recorded_at)
) PARTITION BY RANGE(recorded_at);

CREATE TABLE monitoring_events (
 id varchar(64) PRIMARY KEY, instance_id varchar(64) NOT NULL,
 time timestamptz NOT NULL, time_nano text NOT NULL, action text NOT NULL,
 health_status text NOT NULL DEFAULT '', exit_code integer, signal text NOT NULL DEFAULT ''
);
CREATE INDEX monitoring_events_instance_time ON monitoring_events(instance_id, time, id);
CREATE INDEX monitoring_events_time ON monitoring_events(time);

CREATE TABLE monitoring_gaps (
 id bigserial PRIMARY KEY, node_id text NOT NULL, engine_id text NOT NULL,
 instance_id varchar(64) NOT NULL DEFAULT '',
 since timestamptz NOT NULL, until timestamptz NOT NULL, reason text NOT NULL
);
CREATE INDEX monitoring_gaps_range ON monitoring_gaps(node_id, engine_id, until);

CREATE TABLE monitoring_checkpoints (
 node_id text NOT NULL, engine_id text NOT NULL, since timestamptz NOT NULL,
 PRIMARY KEY(node_id, engine_id)
);
