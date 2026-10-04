# HUMANS.md

Hello fellow humans. This is an LLM-free space, hand-written by the project's
author. In this document I will explain the involvement of LLMs in the
development of Syrinx.

## Here We Are

This is a weird timeline we live in. Having been a programmer for over 20 years
it's completely surreal that we are at a point in which machines are not only
capable of writing code, but that we as humans enter into arguments with each
other over how much of that is acceptable in a project. I would have never
imagined that I would see this in my lifetime.

Since this is such a contentious topic I want to provide full transparency for
you, the reader. I will explain how I approach AI-assisted development.

## The Layers

The level of involvement with AI-generated code differs greatly depending on the
"layer" that this code lives in. Let's say that the most important one is "Layer
0". Everything is this layer is decided by me. I do not simply review the code,
I tell the AI what we are going to do and ask it to challenge that. I propose
and the LLM challenges. I would say that every architectural decision goes here.

Database models fall in this category. I believe that the single most important
part of an algorithm is not the actual algorithm, but the data structures that
supports it. They are like the vase into which you "pour" the algorithm, which
will then take the form of its data structures.

In an HTTP service the database will shape the complexity of your queries,
whether you will need to process the data at the application level after
fetching it, and therefore directly impact the ability of your system to scale.
So I do review every single line of SQL code to setup the DB, every field, every
FK, every index. I have decided which tables are `UNLOGGED` and which ones
aren't. I take full control of it.

The other thing that goes into this theoretical "Layer 0" is everything related
to the security model, i.e. cryptography. As I said earlier, this means that I
don't give the AI a requirement (e.g. "the data needs to be tamper-proof") but
instead I give it concrete tasks to implement. Also, I have a very concrete idea
of what I want and how to achieve it but the AI doesn't seem to understand it at
a conceptual level. So more often than not it would come up with random
corner-cutting solutions that go against the whole philosophy of the project.

One more thing, the event system. I had to design an event system for Syrinx
with at-least-once guarantee, so that the server can be sure that every client
who needs to get an event, will get it. This is the kind of critical path that
you don't want to give up to an AI.

Anyway, moving on to "Layer 1". I would say that secondary architectural
decisions that you can review after the fact fall in this category. For example
endpoints and WS messages. I want to make sure that I understand "the shape" of
the system even if I don't review every single line of code in the handler.
After all, the AI is pretty good at writing HTTP handler code and handling all
possible corner cases, as long as it has a solid foundation in the DB schema.

The frontend data layer is also a part of this imo. On the one hand it's crucial
for the project to have the correct information stored and that all the
necessary cryptograpic material is stored properly as well. Second, storage in
mobile devices is a precious resource (even if the browser reports that the app
can store 10G, the system may not have that storage phisically available, so you
have to be very careful.) So I make sure that the app is always storing the
correct and necessary data only.

Now we move on to more concrete things. Layer 2 would be code that you may not
need to read in detail but at least you should understand that the logic is
correct. For me this is HTTP handlers and SQL queries. As I said earlier, AI
shines at figuring out how to interact with pre-existing things.

The only exception would be a query that could be a source of concurrency
issues, in that case I would pay closer attention, but I've noticed that the
LLMs do pretty good at that (again, if the schema is well designed, they figure
out the rest pretty reliably in my experience.)

Finally, what I would say it's the last layer of this model: The UI and the
documentation. This is not to say that I don't review any of the frontend code,
as I mentioned earlier the data layer (i.e. LocalStorage and IndexedDB) are part
of Layer 1. But the crux of this project is the security model, the UI is there
to support with delivering the PoC. As long as it works correctly I don't really
need to get too involved.

The documentation reads pretty robotic if you ask me, not gonna lie. I think the
biggest problem is that LLMs have a hard time drawing a line between what should
be documented and what is just too much detail. That's why you end up sometimes
with a table with environment variables in your docs for example.

All in all, they are good at spitting out hard data, but you do end up with a
dry documentation website. If you want something that reads easier you need to
put in the effort. I haven't.

## Conclusion

So there you go, this is how I approach AI-assisted coding. I personally do not
consider this as "vibe coded" due to the high involvement in every decision, but
I leave that judgement up to the reader.

To be fair, the breadth that I'm able to cover with AI assistance is much wider,
and the quality of each part is much greater than what I could have ever
achieved by myself. A project of this magnitude would have taken me years to
develop instead of months (realisticly, I would have abandoned the project
before reaching this state) and would be nowhere near as good quality as it is
now.
