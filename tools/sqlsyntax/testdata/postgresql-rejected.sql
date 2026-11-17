-- Statements PostgreSQL 17 rejects with a syntax error. Each case sits under a
-- "-- case:" line.
-- case: comments
select id /* inline */ from student # trailing hash
-- trailing dashes
where id = 1;
-- case: typo-select
selec id from student;
-- case: typo-from
select id form student;
-- case: missing-from-table
select * from where id = 1;
-- case: trailing-comma
select id, name, from student;
-- case: missing-comma
select id name grade from student;
-- case: unbalanced-paren
select count(* from student;
-- case: extra-paren
select (id)) from student;
-- case: group-before-where
select course_id, count(*) from enrollment group by course_id where grade > 1;
-- case: having-before-group
select course_id from enrollment having count(*) > 1 group by course_id;
-- case: order-twice
select * from student order by id order by name;
-- case: join-no-table
select * from student join on student.id = 1;
-- case: unclosed-string
select 'abc from student;
-- case: top
select top 2 * from student;
-- case: insert-no-values
insert into student (id, name) (1, 'x');
-- case: update-no-set
update student name = 'x' where id = 1;
-- case: delete-star
delete * from student;
-- case: create-table-trailing-comma
create table bad1 (a int, b int,);
-- case: create-table-no-type
create table bad2 (a, b int);
-- case: alter-junk
alter table student add;
-- case: case-no-end
select case when id = 1 then 'a' from student;
-- case: in-empty
select * from student where id in ();
-- case: double-where
select * from student where id = 1 where name = 'a';
-- case: keyword-as-column
select from from student;
-- case: limit-expr
select * from student limit;
-- case: between
select * from enrollment where grade between 1;
-- case: window-syntax
select rank() over partition by course_id from enrollment;
-- case: cte-missing-as
with t (select 1) select * from t;
-- case: union-dangling
select 1 union;
-- case: limit-comma
select * from student order by id limit 1, 2;
-- case: backticks
select `s`.`name` as `student name` from `student` as `s`;
-- case: insert-on-duplicate
insert into student (id, name) values (1, 'Ann') on duplicate key update name = values(name);
-- case: replace
replace into student (id, name) values (1, 'Rep');
-- case: create-table
create table tutor (id int not null auto_increment, name varchar(40) not null default 'x', course_id int, hired date, pay decimal(8,2) check (pay >= 0), kind enum('ta','ra') not null, primary key (id), unique key uk (name), constraint fk_t foreign key (course_id) references course(id) on delete cascade on update restrict) engine=InnoDB default charset=utf8mb4;
-- case: update-join
update enrollment e join course c on c.id = e.course_id set e.grade = least(e.grade + 5, 100) where c.code = 'COMP1002';
-- case: delete-join
delete e from enrollment e join student s on s.id = e.student_id where s.name = 'Ada';
-- case: describe
describe student;
-- case: lock-tables
lock tables student read, course write;
-- case: insert-values-row
insert into student values row(30, 'Ro'), row(31, 'Wy');
-- case: double-quoted-string
select "hello" as greeting, 'it''s' as q, 'a\'b' as r;
-- case: pg-backtick
select `name` from student;
-- case: pg-autoincrement
create table bad4 (id int auto_increment primary key);
-- case: pg-dollar-unclosed
create function f() returns int language sql as $$ select 1;
-- case: pg-returning-misplaced
insert into student returning id values (1, 'x');
-- case: pg-on-conflict-malformed
insert into student values (1, 'x') on conflict update set name = 'y';
-- case: pg-distinct-on-malformed
select distinct on course_id * from enrollment;
-- case: pg-cast-malformed
select grade:::int from enrollment;
-- case: pg-filter-malformed
select count(*) filter where grade > 1 from enrollment;
-- case: pg-comment-unclosed
select 1 /* never closed
from student;
